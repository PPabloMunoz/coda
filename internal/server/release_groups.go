package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/ppablomunoz/coda/internal/store"
	"github.com/ppablomunoz/coda/internal/tasks"
)

type updateReleaseBody struct {
	NewReleaseID string `json:"new_release_id"`
}

func (h *Handler) getReleaseGroups() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		queryParams := r.URL.Query()

		limit, offset, err := parseLimitAndOffset(r)
		if err != nil {
			h.log.Debugf("bad request %s %s: %v", r.Method, r.URL.Path, err)
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		artistID, ok := requireOptionalMBID(w, queryParams.Get("artist"))
		if !ok {
			return
		}

		includeRelease := hasInclude(r, "release")

		groupType := strings.ToLower(strings.TrimSpace(queryParams.Get("type")))
		switch groupType {
		case "", "album", "ep", "single", "other":
		default:
			writeError(w, http.StatusBadRequest, "invalid 'type' parameter")
			return
		}

		hasRelease, err := parseOptionalBool(queryParams.Get("has_release"))
		if err != nil {
			h.log.Debugf("bad request %s %s: %v", r.Method, r.URL.Path, err)
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		primary, err := parseOptionalBool(queryParams.Get("primary"))
		if err != nil {
			h.log.Debugf("bad request %s %s: %v", r.Method, r.URL.Path, err)
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if primary != nil && artistID == "" {
			writeError(w, http.StatusBadRequest, "primary requires artist")
			return
		}

		groups, total, err := h.st.ListReleaseGroups(
			r.Context(), store.ReleaseGroupListOptions{
				SearchOptions:  store.SearchOptions{Query: queryParams.Get("q")},
				SortOptions:    store.SortOptions{Sort: queryParams.Get("sort"), Order: queryParams.Get("order")},
				PageOptions:    store.PageOptions{Limit: limit, Offset: offset},
				ArtistID:       artistID,
				IncludeRelease: includeRelease,
				Type:           groupType,
				HasRelease:     hasRelease,
				Primary:        primary,
			})
		if h.handleError(w, err) {
			return
		}

		response := Response[[]store.ReleaseGroupParams]{
			Total: total,
			Items: groups,
		}

		writeJSON(w, http.StatusOK, response)
	}
}

func (h *Handler) getReleaseGroup() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		releaseGroupID, ok := requireMBID(w, r.PathValue("id"))
		if !ok {
			return
		}

		group, err := h.st.GetReleaseGroup(
			r.Context(), releaseGroupID,
		)
		if h.handleError(w, err) {
			return
		}

		writeJSON(w, http.StatusOK, group)
	}
}

func (h *Handler) replaceRelease() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		releaseGroupID, ok := requireMBID(w, r.PathValue("id"))
		if !ok {
			return
		}

		var body updateReleaseBody

		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			h.log.Debugf("bad request %s %s: %v", r.Method, r.URL.Path, err)
			writeError(w, http.StatusBadRequest, "cannot read body")
			return
		}

		newReleaseID, ok := requireMBID(w, body.NewReleaseID)
		if !ok {
			return
		}

		newRelease, err := h.mb.GetReleaseByID(
			ctx, releaseGroupID, newReleaseID,
		)
		if h.handleError(w, err) {
			return
		}

		stRelease := tasks.FullReleaseFromMB(*newRelease)
		if err := h.st.ReplaceRelease(ctx,
			releaseGroupID, stRelease); h.handleError(w, err) {
			return
		}

		writeJSON(w, http.StatusOK, nil)
	}
}

func (h *Handler) deleteReleaseGroup() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		releaseGroupID, ok := requireMBID(w, r.PathValue("id"))
		if !ok {
			return
		}

		if err := h.st.DeleteReleaseGroup(ctx, releaseGroupID); h.handleError(w, err) {
			return
		}

		writeJSON(w, http.StatusOK, nil)
	}
}
