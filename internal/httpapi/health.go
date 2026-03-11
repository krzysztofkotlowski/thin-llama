package httpapi

import "net/http"

func (a *App) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	health := a.runtime.Health()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":            health.OK,
		"runtime_ready": health.RuntimeReady,
		"chat":          health.Chat,
		"embedding":     health.Embedding,
		"runtime": map[string]any{
			"name":         a.build.RuntimeName(),
			"version":      a.build.Version,
			"git_ref":      a.build.GitRef(),
			"build_date":   a.build.Date,
			"capabilities": a.build.Capabilities(),
		},
	})
}
