package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/krzysztofkotlowski/thin-llama/internal/config"
	"github.com/krzysztofkotlowski/thin-llama/internal/metrics"
	"github.com/krzysztofkotlowski/thin-llama/internal/models"
	"github.com/krzysztofkotlowski/thin-llama/internal/pull"
	tlruntime "github.com/krzysztofkotlowski/thin-llama/internal/runtime"
	"github.com/krzysztofkotlowski/thin-llama/internal/state"
)

type stubRuntime struct {
	health    tlruntime.HealthSnapshot
	chat      tlruntime.Target
	chatErr   error
	embedding tlruntime.Target
	embedErr  error
	setErr    error
	store     *state.Store
	lastChat  string
	lastEmbed string
}

func (s *stubRuntime) Health() tlruntime.HealthSnapshot {
	return s.health
}

func (s *stubRuntime) ChatTarget(string) (tlruntime.Target, error) {
	return s.chat, s.chatErr
}

func (s *stubRuntime) EmbeddingTarget(string) (tlruntime.Target, error) {
	return s.embedding, s.embedErr
}

func (s *stubRuntime) SetActiveModels(_ context.Context, chat string, embedding string) error {
	if s.setErr != nil {
		return s.setErr
	}
	s.lastChat = chat
	s.lastEmbed = embedding
	if s.store == nil {
		return nil
	}
	_, err := s.store.Update(func(st *state.State) error {
		if strings.TrimSpace(chat) != "" {
			st.Active.Chat = strings.TrimSpace(chat)
		}
		if strings.TrimSpace(embedding) != "" {
			st.Active.Embedding = strings.TrimSpace(embedding)
		}
		return nil
	})
	return err
}

type stubPuller struct {
	result *pull.Result
	err    error
}

func (s stubPuller) PullModel(context.Context, string) (*pull.Result, error) {
	return s.result, s.err
}

func newTestConfig(dir string) *config.Config {
	return &config.Config{
		ListenAddr:     ":8080",
		StateDir:       filepath.Join(dir, "state"),
		ModelsDir:      dir,
		LlamaServerBin: "/usr/local/bin/llama-server",
		Active: config.ActiveModels{
			Chat:      "qwen2.5:3b",
			Embedding: "all-minilm",
		},
		Models: []config.ModelConfig{
			{Name: "qwen2.5:3b", Role: "chat", GGUFPath: filepath.Join(dir, "chat.gguf")},
			{Name: "all-minilm", Role: "embedding", GGUFPath: filepath.Join(dir, "embed.gguf"), EmbeddingDims: 384},
		},
	}
}

func newHandler(t *testing.T, rt *stubRuntime, puller stubPuller) http.Handler {
	handler, _, _ := newHandlerWithAvailability(t, rt, puller, true, true)
	return handler
}

func newHandlerWithAvailability(t *testing.T, rt *stubRuntime, puller stubPuller, chatAvailable bool, embedAvailable bool) (http.Handler, *state.Store, *config.Config) {
	t.Helper()
	dir := t.TempDir()
	if chatAvailable {
		if err := os.WriteFile(filepath.Join(dir, "chat.gguf"), []byte("chat"), 0o644); err != nil {
			t.Fatalf("WriteFile(chat) unexpected error: %v", err)
		}
	}
	if embedAvailable {
		if err := os.WriteFile(filepath.Join(dir, "embed.gguf"), []byte("embed"), 0o644); err != nil {
			t.Fatalf("WriteFile(embed) unexpected error: %v", err)
		}
	}

	cfg := newTestConfig(dir)
	catalog, err := models.New(cfg)
	if err != nil {
		t.Fatalf("models.New() unexpected error: %v", err)
	}
	store := state.New(cfg.StateDir)
	if _, err := store.Update(func(st *state.State) error {
		st.Active.Chat = cfg.Active.Chat
		st.Active.Embedding = cfg.Active.Embedding
		return nil
	}); err != nil {
		t.Fatalf("store.Update() unexpected error: %v", err)
	}
	if rt == nil {
		rt = &stubRuntime{}
	}
	rt.store = store
	return NewServer(cfg, catalog, rt, puller, store, metrics.New()), store, cfg
}

func TestHealthReflectsRuntimeReadiness(t *testing.T) {
	handler := newHandler(t, &stubRuntime{
		health: tlruntime.HealthSnapshot{
			OK:           true,
			RuntimeReady: false,
		},
	}, stubPuller{})

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"runtime_ready":false`) {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
}

func TestTagsReturnsOnlyDownloadedModels(t *testing.T) {
	handler, _, _ := newHandlerWithAvailability(t, &stubRuntime{health: tlruntime.HealthSnapshot{OK: true}}, stubPuller{}, true, false)

	req := httptest.NewRequest(http.MethodGet, "/api/tags", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "qwen2.5:3b") {
		t.Fatalf("unexpected body: %s", body)
	}
	if strings.Contains(body, "all-minilm") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestChatNonStreamTranslatesResponse(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]any{
						"role":    "assistant",
						"content": "hello from llama.cpp",
					},
				},
			},
		})
	}))
	defer upstream.Close()

	handler := newHandler(t, &stubRuntime{
		health: tlruntime.HealthSnapshot{OK: true},
		chat: tlruntime.Target{
			Model:   config.ModelConfig{Name: "qwen2.5:3b", Role: "chat"},
			BaseURL: upstream.URL,
			Port:    11435,
		},
	}, stubPuller{})

	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(`{"messages":[{"role":"user","content":"hi"}],"stream":false}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "hello from llama.cpp") {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
}

func TestChatStreamTranslatesSSEToNDJSON(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hello \"}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"world\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()

	handler := newHandler(t, &stubRuntime{
		health: tlruntime.HealthSnapshot{OK: true},
		chat: tlruntime.Target{
			Model:   config.ModelConfig{Name: "qwen2.5:3b", Role: "chat"},
			BaseURL: upstream.URL,
			Port:    11435,
		},
	}, stubPuller{})

	server := httptest.NewServer(handler)
	defer server.Close()

	req, err := http.NewRequest(http.MethodPost, server.URL+"/api/chat", strings.NewReader(`{"messages":[{"role":"user","content":"hi"}],"stream":true}`))
	if err != nil {
		t.Fatalf("http.NewRequest() unexpected error: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("Do() unexpected error: %v", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll() unexpected error: %v", err)
	}
	body := string(bodyBytes)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(body, `"content":"hello "`) || !strings.Contains(body, `"content":"world"`) || !strings.Contains(body, `"done":true`) {
		t.Fatalf("unexpected stream body: %s", body)
	}
}

func TestEmbedReturnsEmbeddings(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"embedding": []float64{1, 2, 3}},
				{"embedding": []float64{4, 5, 6}},
			},
		})
	}))
	defer upstream.Close()

	handler := newHandler(t, &stubRuntime{
		health: tlruntime.HealthSnapshot{OK: true},
		embedding: tlruntime.Target{
			Model:   config.ModelConfig{Name: "all-minilm", Role: "embedding", EmbeddingDims: 384},
			BaseURL: upstream.URL,
			Port:    11436,
		},
	}, stubPuller{})

	req := httptest.NewRequest(http.MethodPost, "/api/embed", bytes.NewBufferString(`{"input":["golang","llama"]}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"embeddings"`) {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
}

func TestPullReturnsOllamaLikePayload(t *testing.T) {
	handler := newHandler(t, &stubRuntime{health: tlruntime.HealthSnapshot{OK: true}}, stubPuller{
		result: &pull.Result{
			Model:            "all-minilm",
			Path:             "/models/all-minilm.gguf",
			Downloaded:       true,
			ChecksumVerified: true,
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/api/pull", strings.NewReader(`{"model":"all-minilm"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"status":"success"`) {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
}

func TestModelsReturnsCatalogStatusAndActiveSelection(t *testing.T) {
	handler, store, _ := newHandlerWithAvailability(t, &stubRuntime{health: tlruntime.HealthSnapshot{OK: true}}, stubPuller{}, true, false)
	if _, err := store.Update(func(st *state.State) error {
		st.Downloads["all-minilm"] = state.DownloadStatus{
			ModelName: "all-minilm",
			Status:    "missing",
		}
		return nil
	}); err != nil {
		t.Fatalf("store.Update() unexpected error: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/models", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"chat":"qwen2.5:3b"`) || !strings.Contains(body, `"embedding":"all-minilm"`) {
		t.Fatalf("unexpected active payload: %s", body)
	}
	if !strings.Contains(body, `"name":"qwen2.5:3b"`) || !strings.Contains(body, `"available":true`) {
		t.Fatalf("unexpected chat model payload: %s", body)
	}
	if !strings.Contains(body, `"name":"all-minilm"`) || !strings.Contains(body, `"download_status":"missing"`) {
		t.Fatalf("unexpected embedding model payload: %s", body)
	}
}

func TestActiveModelsEndpointDelegatesAndPersistsState(t *testing.T) {
	rt := &stubRuntime{health: tlruntime.HealthSnapshot{OK: true}}
	handler := newHandler(t, rt, stubPuller{})

	req := httptest.NewRequest(http.MethodPost, "/api/models/active", strings.NewReader(`{"chat":"qwen2.5:3b","embedding":"all-minilm"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if rt.lastChat != "qwen2.5:3b" || rt.lastEmbed != "all-minilm" {
		t.Fatalf("SetActiveModels() received chat=%q embedding=%q", rt.lastChat, rt.lastEmbed)
	}
	if !strings.Contains(rec.Body.String(), `"chat":"qwen2.5:3b"`) || !strings.Contains(rec.Body.String(), `"embedding":"all-minilm"`) {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
}

func TestActiveModelsEndpointReturnsValidationError(t *testing.T) {
	handler := newHandler(t, &stubRuntime{
		health: tlruntime.HealthSnapshot{OK: true},
		setErr: context.DeadlineExceeded,
	}, stubPuller{})

	req := httptest.NewRequest(http.MethodPost, "/api/models/active", strings.NewReader(`{"chat":"qwen2.5:3b"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}
