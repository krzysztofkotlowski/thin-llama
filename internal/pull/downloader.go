package pull

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/krzysztofkotlowski/thin-llama/internal/config"
	"github.com/krzysztofkotlowski/thin-llama/internal/models"
	"github.com/krzysztofkotlowski/thin-llama/internal/state"
)

type Result struct {
	Model            string `json:"model"`
	Path             string `json:"path"`
	Downloaded       bool   `json:"downloaded"`
	ChecksumVerified bool   `json:"checksum_verified"`
}

type Manager struct {
	cfg     *config.Config
	catalog *models.Catalog
	store   *state.Store
	client  *http.Client
}

func NewManager(cfg *config.Config, catalog *models.Catalog, store *state.Store) *Manager {
	return &Manager{
		cfg:     cfg,
		catalog: catalog,
		store:   store,
		client: &http.Client{
			Timeout: 30 * time.Minute,
		},
	}
}

func ResolveModelPath(cfg *config.Config, model config.ModelConfig) string {
	path := strings.TrimSpace(model.GGUFPath)
	if path == "" {
		return filepath.Join(cfg.ModelsDir, sanitizeModelName(model.Name)+".gguf")
	}
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(cfg.ModelsDir, path)
}

func (m *Manager) PullModel(ctx context.Context, modelName string) (*Result, error) {
	model, err := m.catalog.Require(modelName)
	if err != nil {
		return nil, err
	}

	path := ResolveModelPath(m.cfg, model)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create model dir: %w", err)
	}

	if _, err := os.Stat(path); err == nil {
		if err := VerifyFileSHA256(path, model.SHA256); err != nil {
			return nil, err
		}
		now := nowString()
		if _, err := m.store.Update(func(st *state.State) error {
			st.Models[model.Name] = state.ModelState{
				Name:           model.Name,
				LocalPath:      path,
				Available:      true,
				LastChecksumOK: strings.TrimSpace(model.SHA256) != "",
				UpdatedAt:      now,
			}
			st.Downloads[model.Name] = state.DownloadStatus{
				ModelName: model.Name,
				Status:    "available",
				UpdatedAt: now,
			}
			return nil
		}); err != nil {
			return nil, err
		}
		return &Result{
			Model:            model.Name,
			Path:             path,
			Downloaded:       false,
			ChecksumVerified: strings.TrimSpace(model.SHA256) != "",
		}, nil
	}

	sourceURL := strings.TrimSpace(model.SourceURL)
	if sourceURL == "" {
		return nil, fmt.Errorf("model %q is missing locally and has no source_url", model.Name)
	}
	if _, err := url.ParseRequestURI(sourceURL); err != nil {
		return nil, fmt.Errorf("model %q has invalid source_url: %w", model.Name, err)
	}

	if _, err := m.store.Update(func(st *state.State) error {
		st.Downloads[model.Name] = state.DownloadStatus{
			ModelName: model.Name,
			Status:    "downloading",
			UpdatedAt: nowString(),
		}
		return nil
	}); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create download request: %w", err)
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download model: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download model: unexpected status %s", resp.Status)
	}

	tempPath := path + ".download"
	file, err := os.Create(tempPath)
	if err != nil {
		return nil, fmt.Errorf("create temp model file: %w", err)
	}
	if _, err := io.Copy(file, resp.Body); err != nil {
		_ = file.Close()
		_ = os.Remove(tempPath)
		return nil, fmt.Errorf("write model file: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(tempPath)
		return nil, fmt.Errorf("close temp model file: %w", err)
	}

	if err := VerifyFileSHA256(tempPath, model.SHA256); err != nil {
		_ = os.Remove(tempPath)
		_, _ = m.store.Update(func(st *state.State) error {
			st.Downloads[model.Name] = state.DownloadStatus{
				ModelName: model.Name,
				Status:    "error",
				UpdatedAt: nowString(),
				LastError: err.Error(),
			}
			return nil
		})
		return nil, err
	}

	if err := os.Rename(tempPath, path); err != nil {
		_ = os.Remove(tempPath)
		return nil, fmt.Errorf("activate model file: %w", err)
	}

	doneAt := nowString()
	if _, err := m.store.Update(func(st *state.State) error {
		st.Models[model.Name] = state.ModelState{
			Name:             model.Name,
			LocalPath:        path,
			Available:        true,
			LastChecksumOK:   strings.TrimSpace(model.SHA256) != "",
			LastDownloadedAt: doneAt,
			UpdatedAt:        doneAt,
		}
		st.Downloads[model.Name] = state.DownloadStatus{
			ModelName: model.Name,
			Status:    "downloaded",
			UpdatedAt: doneAt,
		}
		return nil
	}); err != nil {
		return nil, err
	}

	return &Result{
		Model:            model.Name,
		Path:             path,
		Downloaded:       true,
		ChecksumVerified: strings.TrimSpace(model.SHA256) != "",
	}, nil
}

func sanitizeModelName(name string) string {
	replacer := strings.NewReplacer("/", "-", "\\", "-", ":", "-", " ", "-")
	return replacer.Replace(strings.ToLower(strings.TrimSpace(name)))
}

func nowString() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}
