package server

import (
	"net/http"
)

// getReleases resolves the release kept for a release-group:
// GET /api/releases?group=<release-group-mbid>. One release per group is
// enforced by the UNIQUE on releases.release_group_id. The group param is
// required; a group with no saved release yet answers 404 null.
func (h *Handler) getReleases() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		releaseGroupID, ok := requireMBID(w, r.URL.Query().Get("group"))
		if !ok {
			return
		}

		release, err := h.st.GetReleaseByGroup(
			r.Context(),
			releaseGroupID,
			hasInclude(r, "files"),
		)
		if h.handleError(w, err) {
			return
		}

		writeJSON(w, http.StatusOK, release)
	}
}

func (h *Handler) getRelease() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		releaseID, ok := requireMBID(w, r.PathValue("id"))
		if !ok {
			return
		}

		release, err := h.st.GetRelease(
			r.Context(),
			releaseID,
			hasInclude(r, "files"),
		)
		if h.handleError(w, err) {
			return
		}

		writeJSON(w, http.StatusOK, release)
	}
}
