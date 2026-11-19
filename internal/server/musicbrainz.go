package server

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/ppablomunoz/coda/internal/musicbrainz"
)

func (h *Handler) searchMusicBrainzArtists() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		query := strings.TrimSpace(r.URL.Query().Get("q"))
		if len(query) < 2 {
			writeError(
				w,
				http.StatusBadRequest,
				"'q' must be at least 2 characters",
			)
			return
		}

		limit := 10
		if raw := r.URL.Query().Get("limit"); raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil || value < 1 || value > 25 {
				writeError(
					w,
					http.StatusBadRequest,
					"'limit' must be between 1 and 25",
				)
				return
			}
			limit = value
		}

		artists, err := h.mb.SearchArtists(r.Context(), query, limit)
		if err != nil {
			if errors.Is(err, musicbrainz.ErrNotFound) {
				h.log.Debugf("musicbrainz not found: %v", err)
				writeJSON(w, http.StatusNotFound, nil)
				return
			}
			h.log.Errorf("musicbrainz search: %v", err)
			writeError(w, http.StatusBadGateway, "MusicBrainz search failed")
			return
		}

		response := Response[[]musicbrainz.ArtistSearchResult]{
			Total: len(artists),
			Items: artists,
		}
		writeJSON(w, http.StatusOK, response)
	}
}
