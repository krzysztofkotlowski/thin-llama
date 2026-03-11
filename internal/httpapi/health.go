package httpapi

import "net/http"

func (a *App) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	snapshot := a.runtime.Snapshot()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":            snapshot.OK,
		"runtime_ready": snapshot.RuntimeReady,
		"active": map[string]string{
			"chat":      snapshot.Active.Chat,
			"embedding": snapshot.Active.Embedding,
		},
		"chat":      snapshot.Chat,
		"embedding": snapshot.Embedding,
		"runtime": map[string]any{
			"name":         a.build.RuntimeName(),
			"version":      a.build.Version,
			"git_ref":      a.build.GitRef(),
			"build_date":   a.build.Date,
			"capabilities": a.build.Capabilities(),
		},
	})
}
