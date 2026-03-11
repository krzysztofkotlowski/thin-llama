package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	tlruntime "github.com/krzysztofkotlowski/thin-llama/internal/runtime"
)

type ollamaMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ollamaChatRequest struct {
	Model    string                 `json:"model"`
	Messages []ollamaMessage        `json:"messages"`
	Stream   bool                   `json:"stream"`
	Options  map[string]interface{} `json:"options,omitempty"`
}

type openAIChatRequest struct {
	Model       string          `json:"model"`
	Messages    []ollamaMessage `json:"messages"`
	Stream      bool            `json:"stream"`
	Temperature *float64        `json:"temperature,omitempty"`
	MaxTokens   *int            `json:"max_tokens,omitempty"`
}

type openAIChatResponse struct {
	Choices []struct {
		Message ollamaMessage `json:"message"`
	} `json:"choices"`
}

func (a *App) handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var request ollamaChatRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if len(request.Messages) == 0 {
		writeError(w, http.StatusBadRequest, "messages are required")
		return
	}

	target, err := a.runtime.ChatTarget(strings.TrimSpace(request.Model))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	proxyBody := openAIChatRequest{
		Model:    target.Model.Name,
		Messages: request.Messages,
		Stream:   request.Stream,
	}
	if temperature, ok := floatFromMap(request.Options, "temperature"); ok {
		proxyBody.Temperature = &temperature
	}
	if maxTokens, ok := intFromMap(request.Options, "num_predict"); ok {
		proxyBody.MaxTokens = &maxTokens
	}
	if maxTokens, ok := intFromMap(request.Options, "max_tokens"); ok {
		proxyBody.MaxTokens = &maxTokens
	}

	payload, err := json.Marshal(proxyBody)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to encode upstream request")
		return
	}

	if request.Stream {
		a.streamChat(w, r.Context(), target, payload)
		return
	}

	upstreamReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, target.BaseURL+"/v1/chat/completions", bytes.NewReader(payload))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create upstream request")
		return
	}
	upstreamReq.Header.Set("Content-Type", "application/json")

	resp, err := a.client.Do(upstreamReq)
	if err != nil {
		a.metrics.ProxyFailures.WithLabelValues("/api/chat").Inc()
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		a.metrics.ProxyFailures.WithLabelValues("/api/chat").Inc()
		writeError(w, http.StatusBadGateway, fmt.Sprintf("upstream returned %s", resp.Status))
		return
	}

	var upstream openAIChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&upstream); err != nil {
		a.metrics.ProxyFailures.WithLabelValues("/api/chat").Inc()
		writeError(w, http.StatusBadGateway, "invalid upstream response")
		return
	}
	content := ""
	if len(upstream.Choices) > 0 {
		content = upstream.Choices[0].Message.Content
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"model":      target.Model.Name,
		"created_at": time.Now().UTC().Format(time.RFC3339Nano),
		"message": map[string]string{
			"role":    "assistant",
			"content": content,
		},
		"done": true,
	})
}

func (a *App) streamChat(w http.ResponseWriter, ctx context.Context, target tlruntime.Target, payload []byte) {
	upstreamReq, err := http.NewRequestWithContext(ctx, http.MethodPost, target.BaseURL+"/v1/chat/completions", bytes.NewReader(payload))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create upstream request")
		return
	}
	upstreamReq.Header.Set("Content-Type", "application/json")

	resp, err := a.client.Do(upstreamReq)
	if err != nil {
		a.metrics.ProxyFailures.WithLabelValues("/api/chat").Inc()
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		a.metrics.ProxyFailures.WithLabelValues("/api/chat").Inc()
		writeError(w, http.StatusBadGateway, fmt.Sprintf("upstream returned %s", resp.Status))
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming is unsupported")
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.WriteHeader(http.StatusOK)

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"model":      target.Model.Name,
				"created_at": time.Now().UTC().Format(time.RFC3339Nano),
				"message": map[string]string{
					"role":    "assistant",
					"content": "",
				},
				"done": true,
			})
			flusher.Flush()
			return
		}

		var payload struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &payload); err != nil {
			continue
		}
		chunk := ""
		if len(payload.Choices) > 0 {
			chunk = payload.Choices[0].Delta.Content
		}
		if chunk == "" {
			continue
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model":      target.Model.Name,
			"created_at": time.Now().UTC().Format(time.RFC3339Nano),
			"message": map[string]string{
				"role":    "assistant",
				"content": chunk,
			},
			"done": false,
		})
		flusher.Flush()
	}
	if err := scanner.Err(); err != nil {
		a.metrics.ProxyFailures.WithLabelValues("/api/chat").Inc()
	}
}

func floatFromMap(values map[string]interface{}, key string) (float64, bool) {
	if values == nil {
		return 0, false
	}
	value, ok := values[key]
	if !ok {
		return 0, false
	}
	switch v := value.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	default:
		return 0, false
	}
}

func intFromMap(values map[string]interface{}, key string) (int, bool) {
	if values == nil {
		return 0, false
	}
	value, ok := values[key]
	if !ok {
		return 0, false
	}
	switch v := value.(type) {
	case float64:
		return int(v), true
	case int:
		return v, true
	default:
		return 0, false
	}
}
