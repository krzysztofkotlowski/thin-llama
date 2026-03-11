package httpapi

import (
	"encoding/json"
	"net/http"
	"os"

	"github.com/krzysztofkotlowski/thin-llama/internal/pull"
)

type activeModelsRequest struct {
	Chat      string `json:"chat"`
	Embedding string `json:"embedding"`
}

func (a *App) handleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	current, err := a.store.Load()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	snapshot := a.runtime.Snapshot()

	modelsOut := make([]map[string]any, 0, len(a.catalog.All()))
	for _, model := range a.catalog.All() {
		path := pull.ResolveModelPath(a.cfg, model)
		available := false
		if _, err := os.Stat(path); err == nil {
			available = true
		}

		downloadStatus := current.Downloads[model.Name]
		modelState := current.Models[model.Name]
		active := (model.Role == "chat" && snapshot.Active.Chat == model.Name) || (model.Role == "embedding" && snapshot.Active.Embedding == model.Name)

		roleHealth := snapshot.Embedding
		if model.Role == "chat" {
			roleHealth = snapshot.Chat
		}

		runtimeRunning := active && roleHealth.ModelName == model.Name && roleHealth.Running
		runtimeReady := false
		runtimeError := ""
		runtimePID := 0
		runtimeMessage := ""
		orphanDetected := false
		restartCount := 0
		restartSuppressed := false
		if active && roleHealth.ModelName == model.Name {
			runtimeRunning = roleHealth.Running
			runtimeReady = roleHealth.Ready
			runtimeError = roleHealth.LastError
			runtimePID = roleHealth.PID
			runtimeMessage = roleHealth.StatusMessage
			orphanDetected = roleHealth.OrphanDetected
			restartCount = roleHealth.RestartCount
			restartSuppressed = roleHealth.RestartSuppressed
		}

		modelsOut = append(modelsOut, map[string]any{
			"name":               model.Name,
			"role":               model.Role,
			"embedding_dims":     model.EmbeddingDims,
			"available":          available,
			"active":             active,
			"path":               path,
			"download_status":    downloadStatus.Status,
			"download_error":     downloadStatus.LastError,
			"last_downloaded_at": modelState.LastDownloadedAt,
			"runtime_running":    runtimeRunning,
			"runtime_ready":      runtimeReady,
			"runtime_error":      runtimeError,
			"runtime_pid":        runtimePID,
			"runtime_message":    runtimeMessage,
			"orphan_detected":    orphanDetected,
			"restart_count":      restartCount,
			"restart_suppressed": restartSuppressed,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"active": map[string]string{
			"chat":      snapshot.Active.Chat,
			"embedding": snapshot.Active.Embedding,
		},
		"models": modelsOut,
	})
}

func (a *App) handleActiveModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var request activeModelsRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}

	if err := a.runtime.SetActiveModels(r.Context(), request.Chat, request.Embedding); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	current, err := a.store.Load()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"active": map[string]string{
			"chat":      current.Active.Chat,
			"embedding": current.Active.Embedding,
		},
		"health": a.runtime.Health(),
	})
}
