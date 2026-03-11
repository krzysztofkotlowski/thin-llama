package httpapi

import "net/http"

func (a *App) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	health := a.runtime.Health()
	writeJSON(w, http.StatusOK, health)
}
