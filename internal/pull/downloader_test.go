package pull

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/krzysztofkotlowski/thin-llama/internal/config"
	"github.com/krzysztofkotlowski/thin-llama/internal/models"
	"github.com/krzysztofkotlowski/thin-llama/internal/state"
)

func newTestManager(t *testing.T, model config.ModelConfig) (*Manager, *state.Store, string) {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{
		ListenAddr:     ":8080",
		StateDir:       filepath.Join(dir, "state"),
		ModelsDir:      filepath.Join(dir, "models"),
		LlamaServerBin: "/usr/local/bin/llama-server",
		Models:         []config.ModelConfig{model},
	}
	catalog, err := models.New(cfg)
	if err != nil {
		t.Fatalf("models.New() unexpected error: %v", err)
	}
	store := state.New(cfg.StateDir)
	return NewManager(cfg, catalog, store), store, dir
}

func TestPullModelMarksExistingChecksumMismatchAsError(t *testing.T) {
	model := config.ModelConfig{
		Name:     "test-chat",
		Role:     "chat",
		GGUFPath: "test-chat.gguf",
		SHA256:   "deadbeef",
	}
	manager, store, dir := newTestManager(t, model)
	path := filepath.Join(dir, "models", model.GGUFPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() unexpected error: %v", err)
	}
	if err := os.WriteFile(path, []byte("bad-model"), 0o644); err != nil {
		t.Fatalf("WriteFile() unexpected error: %v", err)
	}

	_, err := manager.PullModel(context.Background(), model.Name)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("PullModel() expected checksum mismatch, got %v", err)
	}

	st, err := store.Load()
	if err != nil {
		t.Fatalf("store.Load() unexpected error: %v", err)
	}
	if got := st.Downloads[model.Name].Status; got != "error" {
		t.Fatalf("download status = %q, want error", got)
	}
	if !strings.Contains(st.Downloads[model.Name].LastError, "checksum mismatch") {
		t.Fatalf("download last_error = %q, want checksum mismatch", st.Downloads[model.Name].LastError)
	}
	if st.Models[model.Name].Available {
		t.Fatal("model should be unavailable after checksum mismatch")
	}
}

func TestPullModelMarksHTTPFailureAsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "upstream failure", http.StatusInternalServerError)
	}))
	defer server.Close()

	model := config.ModelConfig{
		Name:          "test-embed",
		Role:          "embedding",
		GGUFPath:      "test-embed.gguf",
		SourceURL:     server.URL,
		SHA256:        "deadbeef",
		EmbeddingDims: 384,
	}
	manager, store, _ := newTestManager(t, model)

	_, err := manager.PullModel(context.Background(), model.Name)
	if err == nil || !strings.Contains(err.Error(), "unexpected status 500") {
		t.Fatalf("PullModel() expected HTTP status failure, got %v", err)
	}

	st, err := store.Load()
	if err != nil {
		t.Fatalf("store.Load() unexpected error: %v", err)
	}
	if got := st.Downloads[model.Name].Status; got != "error" {
		t.Fatalf("download status = %q, want error", got)
	}
	if !strings.Contains(st.Downloads[model.Name].LastError, "unexpected status 500") {
		t.Fatalf("download last_error = %q, want HTTP status error", st.Downloads[model.Name].LastError)
	}
	if st.Models[model.Name].Available {
		t.Fatal("model should be unavailable after HTTP failure")
	}
}
