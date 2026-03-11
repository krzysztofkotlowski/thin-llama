package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type ollamaEmbedRequest struct {
	Model string      `json:"model"`
	Input interface{} `json:"input"`
}

type openAIEmbeddingsRequest struct {
	Model string      `json:"model"`
	Input interface{} `json:"input"`
}

func (a *App) handleEmbed(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var request ollamaEmbedRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if request.Input == nil {
		writeError(w, http.StatusBadRequest, "input is required")
		return
	}

	target, err := a.runtime.EmbeddingTarget(strings.TrimSpace(request.Model))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	payload, err := json.Marshal(openAIEmbeddingsRequest{
		Model: target.Model.Name,
		Input: request.Input,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to encode upstream request")
		return
	}

	upstreamReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, target.BaseURL+"/v1/embeddings", bytes.NewReader(payload))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create upstream request")
		return
	}
	upstreamReq.Header.Set("Content-Type", "application/json")

	resp, err := a.client.Do(upstreamReq)
	if err != nil {
		a.metrics.ProxyFailures.WithLabelValues("/api/embed").Inc()
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		a.metrics.ProxyFailures.WithLabelValues("/api/embed").Inc()
		writeError(w, http.StatusBadGateway, fmt.Sprintf("upstream returned %s", resp.Status))
		return
	}

	var upstream struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&upstream); err != nil {
		a.metrics.ProxyFailures.WithLabelValues("/api/embed").Inc()
		writeError(w, http.StatusBadGateway, "invalid upstream response")
		return
	}

	embeddings := make([][]float64, 0, len(upstream.Data))
	for _, item := range upstream.Data {
		embeddings = append(embeddings, item.Embedding)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"model":      target.Model.Name,
		"embeddings": embeddings,
	})
}
