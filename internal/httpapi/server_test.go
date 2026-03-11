package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/krzysztofkotlowski/thin-llama/internal/config"
	"github.com/krzysztofkotlowski/thin-llama/internal/metrics"
	"github.com/krzysztofkotlowski/thin-llama/internal/models"
	"github.com/krzysztofkotlowski/thin-llama/internal/pull"
	tlruntime "github.com/krzysztofkotlowski/thin-llama/internal/runtime"
)

type stubRuntime struct {
	health    tlruntime.HealthSnapshot
	chat      tlruntime.Target
	chatErr   error
	embedding tlruntime.Target
	embedErr  error
}

func (s stubRuntime) Health() tlruntime.HealthSnapshot {
	return s.health
}

func (s stubRuntime) ChatTarget(string) (tlruntime.Target, error) {
	return s.chat, s.chatErr
}

func (s stubRuntime) EmbeddingTarget(string) (tlruntime.Target, error) {
	return s.embedding, s.embedErr
}

type stubPuller struct {
	result *pull.Result
	err    error
}

func (s stubPuller) PullModel(context.Context, string) (*pull.Result, error) {
	return s.result, s.err
}

func newTestConfig() *config.Config {
	return &config.Config{
		ListenAddr:     ":8080",
		StateDir:       "/state",
		ModelsDir:      "/models",
		LlamaServerBin: "/usr/local/bin/llama-server",
		Active: config.ActiveModels{
			Chat:      "qwen2.5:3b",
			Embedding: "all-minilm",
		},
		Models: []config.ModelConfig{
			{Name: "qwen2.5:3b", Role: "chat", GGUFPath: "/models/chat.gguf"},
			{Name: "all-minilm", Role: "embedding", GGUFPath: "/models/embed.gguf", EmbeddingDims: 384},
		},
	}
}

func newHandler(t *testing.T, rt stubRuntime, puller stubPuller) http.Handler {
	t.Helper()
	catalog, err := models.New(newTestConfig())
	if err != nil {
		t.Fatalf("models.New() unexpected error: %v", err)
	}
	return NewServer(newTestConfig(), catalog, rt, puller, metrics.New())
}

func TestHealthReflectsRuntimeReadiness(t *testing.T) {
	handler := newHandler(t, stubRuntime{
		health: tlruntime.HealthSnapshot{
			OK: false,
		},
	}, stubPuller{})

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestTagsReturnsConfiguredModels(t *testing.T) {
	handler := newHandler(t, stubRuntime{health: tlruntime.HealthSnapshot{OK: true}}, stubPuller{})

	req := httptest.NewRequest(http.MethodGet, "/api/tags", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "qwen2.5:3b") || !strings.Contains(body, "all-minilm") {
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

	handler := newHandler(t, stubRuntime{
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

	handler := newHandler(t, stubRuntime{
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

	handler := newHandler(t, stubRuntime{
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
	handler := newHandler(t, stubRuntime{health: tlruntime.HealthSnapshot{OK: true}}, stubPuller{
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
