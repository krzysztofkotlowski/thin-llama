package httpapi

import "net/http"

func (a *App) handleTags(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	modelsOut := make([]map[string]any, 0, len(a.catalog.All()))
	for _, model := range a.catalog.All() {
		modelsOut = append(modelsOut, map[string]any{
			"name":  model.Name,
			"model": model.Name,
			"role":  model.Role,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": modelsOut})
}
