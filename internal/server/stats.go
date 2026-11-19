package server

import "net/http"

func (h *Handler) libraryStats() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		stats, err := h.st.GetLibraryStats(r.Context())
		if h.handleError(w, err) {
			return
		}
		writeJSON(w, http.StatusOK, stats)
	}
}
