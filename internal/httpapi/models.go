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
	health := a.runtime.Health()

	modelsOut := make([]map[string]any, 0, len(a.catalog.All()))
	for _, model := range a.catalog.All() {
		path := pull.ResolveModelPath(a.cfg, model)
		available := false
		if _, err := os.Stat(path); err == nil {
			available = true
		}

		downloadStatus := current.Downloads[model.Name]
		modelState := current.Models[model.Name]
		processState := current.Processes[model.Role]
		active := (model.Role == "chat" && current.Active.Chat == model.Name) || (model.Role == "embedding" && current.Active.Embedding == model.Name)

		runtimeRunning := processState.ModelName == model.Name && processState.Running
		runtimeReady := false
		runtimeError := ""
		if model.Role == "chat" && health.Chat.ModelName == model.Name {
			runtimeRunning = health.Chat.Running
			runtimeReady = health.Chat.Ready
			runtimeError = health.Chat.LastError
		}
		if model.Role == "embedding" && health.Embedding.ModelName == model.Name {
			runtimeRunning = health.Embedding.Running
			runtimeReady = health.Embedding.Ready
			runtimeError = health.Embedding.LastError
		}
		if runtimeError == "" && processState.ModelName == model.Name {
			runtimeError = processState.LastError
		}

		modelsOut = append(modelsOut, map[string]any{
			"name":               model.Name,
			"role":               model.Role,
			"available":          available,
			"active":             active,
			"path":               path,
			"download_status":    downloadStatus.Status,
			"download_error":     downloadStatus.LastError,
			"last_downloaded_at": modelState.LastDownloadedAt,
			"runtime_running":    runtimeRunning,
			"runtime_ready":      runtimeReady,
			"runtime_error":      runtimeError,
			"restart_count":      processState.RestartCount,
			"restart_suppressed": processState.ModelName == model.Name && processState.RestartSuppressed,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"active": map[string]string{
			"chat":      current.Active.Chat,
			"embedding": current.Active.Embedding,
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
