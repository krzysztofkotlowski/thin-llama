package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
)

type ollamaPullRequest struct {
	Model  string `json:"model"`
	Stream bool   `json:"stream"`
}

func (a *App) handlePull(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var request ollamaPullRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	modelName := strings.TrimSpace(request.Model)
	if modelName == "" {
		writeError(w, http.StatusBadRequest, "model is required")
		return
	}

	if request.Stream {
		if err := a.puller.PullModelAsync(modelName); err != nil {
			a.metrics.ModelPulls.WithLabelValues(modelName, "error").Inc()
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		a.metrics.ModelPulls.WithLabelValues(modelName, "started").Inc()
		writeJSON(w, http.StatusAccepted, map[string]any{
			"status":  "started",
			"model":   modelName,
			"message": "Download started in background. Poll GET /api/models for download_status.",
		})
		return
	}

	result, err := a.puller.PullModel(r.Context(), modelName)
	if err != nil {
		a.metrics.ModelPulls.WithLabelValues(modelName, "error").Inc()
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	pullState := "downloaded"
	if !result.Downloaded {
		pullState = "already-present"
	}
	a.metrics.ModelPulls.WithLabelValues(modelName, pullState).Inc()
	writeJSON(w, http.StatusOK, map[string]any{
		"status":            "success",
		"pull_state":        pullState,
		"downloaded":        result.Downloaded,
		"model":             result.Model,
		"path":              result.Path,
		"checksum_verified": result.ChecksumVerified,
	})
}
