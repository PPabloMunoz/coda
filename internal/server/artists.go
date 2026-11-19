package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/ppablomunoz/coda/internal/store"
	"github.com/ppablomunoz/coda/internal/tasks"
)

type PostArtistBody struct {
	ID string `json:"id"`
}

func (h *Handler) getArtists() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit, offset, err := parseLimitAndOffset(r)
		if err != nil {
			h.log.Debugf("bad request %s %s: %v", r.Method, r.URL.Path, err)
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		artists, total, err := h.st.ListArtists(
			r.Context(), store.ArtistListOptions{
				SearchOptions:     store.SearchOptions{Query: r.URL.Query().Get("q")},
				SortOptions:       store.SortOptions{Sort: r.URL.Query().Get("sort"), Order: r.URL.Query().Get("order")},
				PageOptions:       store.PageOptions{Limit: limit, Offset: offset},
				IncludeTrackStats: hasInclude(r, "track_stats"),
				All:               r.URL.Query().Get("all") == "true",
			})
		if h.handleError(w, err) {
			return
		}

		response := Response[[]store.Artist]{
			Total: total,
			Items: artists,
		}

		writeJSON(w, http.StatusOK, response)
	}
}

func (h *Handler) getArtist() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		artistID, ok := requireMBID(w, r.PathValue("id"))
		if !ok {
			return
		}

		artist, err := h.st.GetArtistByID(r.Context(), artistID)
		if h.handleError(w, err) {
			return
		}

		// Collaboration-only artists are hidden unless ?all=true.
		if r.URL.Query().Get("all") != "true" && artist.InLibrary != 1 {
			h.handleError(w, store.ErrNotFound)
			return
		}

		if hasInclude(r, "track_stats") {
			stats, err := h.st.GetArtistTrackStats(
				r.Context(), artistID,
			)
			if h.handleError(w, err) {
				return
			}
			artist.Stats = stats
		}

		writeJSON(w, http.StatusOK, artist)
	}
}

func (h *Handler) getArtistImportStatus() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		artistID, ok := requireMBID(w, r.PathValue("id"))
		if !ok {
			return
		}

		art, err := h.st.GetArtistByID(r.Context(), artistID)
		if h.handleError(w, err) {
			return
		}

		// Collaboration-only artists are hidden unless ?all=true.
		if r.URL.Query().Get("all") != "true" && art.InLibrary != 1 {
			h.handleError(w, store.ErrNotFound)
			return
		}

		writeJSON(w, http.StatusOK, h.queue.ArtistImportStatus(artistID))
	}
}

func (h *Handler) refreshArtist() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		artistID, ok := requireMBID(w, r.PathValue("id"))
		if !ok {
			return
		}

		art, err := h.st.GetArtistByID(r.Context(), artistID)
		if h.handleError(w, err) {
			return
		}

		// Discography imports require library membership.
		if art.InLibrary != 1 {
			h.handleError(w, store.ErrNotFound)
			return
		}

		status := h.queue.EnqueueRefreshArtist(artistID)
		writeJSON(w, http.StatusAccepted, status)
	}
}

func (h *Handler) postArtist() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		var body PostArtistBody

		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			h.log.Debugf("bad request %s %s: %v", r.Method, r.URL.Path, err)
			writeError(w, http.StatusBadRequest, "cannot read body")
			return
		}

		artistID, ok := requireMBID(w, body.ID)
		if !ok {
			return
		}

		// Check DB
		art, err := h.st.GetArtistByID(ctx, artistID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			h.log.Error(err.Error())
			writeError(w, http.StatusInternalServerError, "cannot check db")
			return
		}

		if art != nil && art.InLibrary == 1 {
			h.queue.EnqueueRefreshArtist(art.ID)
			writeJSON(w, http.StatusNotModified, nil)
			return
		}
		promoting := art != nil

		// Fetch MusicBrainz
		artist, err := h.mb.GetArtistByID(ctx, artistID)
		if h.handleError(w, err) {
			return
		}

		stArtist := tasks.ArtistFromMB(artist)
		if err := h.st.CreateArtist(ctx, stArtist); h.handleError(w, err) {
			return
		}

		// Import discography (CreateArtist marks the row in_library = 1).
		h.queue.EnqueueRefreshArtist(stArtist.ID)

		if promoting {
			writeJSON(w, http.StatusOK, stArtist)
			return
		}
		writeJSON(w, http.StatusCreated, stArtist)
	}
}

func (h *Handler) deleteArtist() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		artistID, ok := requireMBID(w, r.PathValue("id"))
		if !ok {
			return
		}

		pruned, err := h.st.DeleteArtist(ctx, artistID)
		if h.handleError(w, err) {
			return
		}

		writeJSON(w, http.StatusOK, map[string]int64{"pruned": pruned})
	}
}
