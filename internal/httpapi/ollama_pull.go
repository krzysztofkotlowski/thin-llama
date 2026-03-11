package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
)

type ollamaPullRequest struct {
	Model string `json:"model"`
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

	result, err := a.puller.PullModel(r.Context(), modelName)
	if err != nil {
		a.metrics.ModelPulls.WithLabelValues(modelName, "error").Inc()
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	status := "success"
	if !result.Downloaded {
		status = "already-present"
	}
	a.metrics.ModelPulls.WithLabelValues(modelName, status).Inc()
	writeJSON(w, http.StatusOK, map[string]any{
		"status":            status,
		"model":             result.Model,
		"path":              result.Path,
		"checksum_verified": result.ChecksumVerified,
	})
}
