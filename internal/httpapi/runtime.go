package httpapi

import "net/http"

func (a *App) handleRuntime(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"runtime":      a.build.RuntimeName(),
		"name":         a.build.RuntimeName(),
		"version":      a.build.Version,
		"git_ref":      a.build.GitRef(),
		"build_date":   a.build.Date,
		"capabilities": a.build.Capabilities(),
	})
}
