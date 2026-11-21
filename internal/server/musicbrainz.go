package server

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	apperrors "github.com/ppablomunoz/coda/internal/errors"
	"github.com/ppablomunoz/coda/internal/musicbrainz"
)

func (h *Handler) searchMusicBrainzArtists(c *gin.Context) {
	ctx := c.Request.Context()

	query := strings.TrimSpace(c.Query("q"))
	if len(query) < 2 {
		h.writeAppError(c, apperrors.Wrap(
			apperrors.ErrInvalidInput,
			fmt.Errorf("'q' must be at least 2 characters"),
		))
		return
	}

	limit := 10
	if raw := c.Query("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 25 {
			h.writeAppError(c, apperrors.Wrap(
				apperrors.ErrInvalidInput,
				fmt.Errorf("'limit' must be between 1 and 25"),
			))
			return
		}
		limit = value
	}

	artists, err := h.mb.SearchArtists(ctx, query, limit)
	if err != nil {
		if apperrors.Is(err, apperrors.ErrNotFound) {
			h.writeAppError(c, err)
			return
		}
		h.writeAppError(c, apperrors.Wrap(
			apperrors.ErrMusicBrainzFailed,
			fmt.Errorf("MusicBrainz search failed"),
		))
		return
	}

	response := Response[[]musicbrainz.ArtistSearchResult]{
		Total: len(artists),
		Items: artists,
	}
	c.JSON(http.StatusOK, response)
}
