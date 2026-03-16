package pull

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestPullModelResumableDownload(t *testing.T) {
	fullContent := []byte("ABCDEFGHIJ")
	var rangeHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rangeHeader = r.Header.Get("Range")
		if rangeHeader != "" {
			if !strings.HasPrefix(rangeHeader, "bytes=") || !strings.HasSuffix(rangeHeader, "-") {
				http.Error(w, "invalid range", http.StatusBadRequest)
				return
			}
			startStr := strings.TrimPrefix(strings.TrimSuffix(rangeHeader, "-"), "bytes=")
			var start int
			if _, err := fmt.Sscanf(startStr, "%d", &start); err != nil || start < 0 || start >= len(fullContent) {
				http.Error(w, "invalid range start", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, len(fullContent)-1, len(fullContent)))
			w.WriteHeader(http.StatusPartialContent)
			w.Write(fullContent[start:])
		} else {
			w.WriteHeader(http.StatusOK)
			w.Write(fullContent)
		}
	}))
	defer server.Close()

	model := config.ModelConfig{
		Name:          "test-resume",
		Role:          "embedding",
		GGUFPath:      "test-resume.gguf",
		SourceURL:     server.URL,
		SHA256:        "",
		EmbeddingDims: 384,
	}
	manager, _, dir := newTestManager(t, model)
	modelsDir := filepath.Join(dir, "models")
	if err := os.MkdirAll(modelsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() unexpected error: %v", err)
	}
	tempPath := filepath.Join(modelsDir, "test-resume.gguf.download")
	if err := os.WriteFile(tempPath, fullContent[:5], 0o644); err != nil {
		t.Fatalf("WriteFile() unexpected error: %v", err)
	}

	result, err := manager.PullModel(context.Background(), model.Name)
	if err != nil {
		t.Fatalf("PullModel() unexpected error: %v", err)
	}
	if !result.Downloaded {
		t.Fatal("PullModel() expected Downloaded=true for resumable download")
	}
	if rangeHeader != "bytes=5-" {
		t.Fatalf("Range header = %q, want bytes=5-", rangeHeader)
	}

	finalPath := filepath.Join(modelsDir, "test-resume.gguf")
	got, err := os.ReadFile(finalPath)
	if err != nil {
		t.Fatalf("ReadFile() unexpected error: %v", err)
	}
	if string(got) != string(fullContent) {
		t.Fatalf("final file content = %q, want %q", got, fullContent)
	}
}

func TestPullModelAsyncStartsBackgroundAndIsIdempotent(t *testing.T) {
	downloadStarted := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(downloadStarted)
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("model-content"))
	}))
	defer server.Close()

	model := config.ModelConfig{
		Name:          "test-async",
		Role:          "embedding",
		GGUFPath:      "test-async.gguf",
		SourceURL:     server.URL,
		SHA256:        "",
		EmbeddingDims: 384,
	}
	manager, store, _ := newTestManager(t, model)

	if err := manager.PullModelAsync(model.Name); err != nil {
		t.Fatalf("PullModelAsync() unexpected error: %v", err)
	}
	select {
	case <-downloadStarted:
		// Good
	case <-time.After(2 * time.Second):
		t.Fatal("PullModelAsync() did not start download within 2s")
	}

	// Idempotent: second call should not error
	if err := manager.PullModelAsync(model.Name); err != nil {
		t.Fatalf("PullModelAsync() second call unexpected error: %v", err)
	}

	// Wait for download to complete
	time.Sleep(200 * time.Millisecond)
	st, err := store.Load()
	if err != nil {
		t.Fatalf("store.Load() unexpected error: %v", err)
	}
	ds := st.Downloads[model.Name]
	if ds.Status != "downloaded" && ds.Status != "error" {
		t.Logf("download status = %q (may still be in progress)", ds.Status)
	}
}
