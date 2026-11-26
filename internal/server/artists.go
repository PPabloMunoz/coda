package server

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"

	apperrors "github.com/ppablomunoz/coda/internal/errors"
	"github.com/ppablomunoz/coda/internal/store"
	"github.com/ppablomunoz/coda/internal/tasks"
)

// getArtists lists library (or all with ?all=true) artists with pagination and optional track stats.
func (h *Handler) getArtists(c *gin.Context) {
	limit, offset, err := parseLimitAndOffset(c)
	if err != nil {
		h.writeAppError(c, err)
		return
	}

	artists, total, err := h.st.ListArtists(
		c.Request.Context(), store.ArtistListOptions{
			SearchOptions:     store.SearchOptions{Query: c.Query("q")},
			SortOptions:       store.SortOptions{Sort: c.Query("sort"), Order: c.Query("order")},
			PageOptions:       store.PageOptions{Limit: limit, Offset: offset},
			IncludeTrackStats: hasInclude(c, "track_stats"),
			All:               c.Query("all") == "true",
		})
	if err != nil {
		h.writeAppError(c, err)
		return
	}

	response := Response[[]store.Artist]{
		Total: total,
		Items: artists,
	}

	c.JSON(http.StatusOK, response)
}

// getArtist returns one artist, hiding collaboration-only rows unless ?all=true.
func (h *Handler) getArtist(c *gin.Context) {
	artistID, err := requireMBID(c.Param("id"))
	if err != nil {
		h.writeAppError(c, err)
		return
	}

	artist, err := h.st.GetArtistByID(c.Request.Context(), artistID)
	if err != nil {
		h.writeAppError(c, err)
		return
	}

	// Collaboration-only artists are hidden unless ?all=true.
	if c.Query("all") != "true" && artist.InLibrary != 1 {
		h.writeAppError(c, apperrors.ErrNotFound)
		return
	}

	if hasInclude(c, "track_stats") {
		stats, err := h.st.GetArtistTrackStats(
			c.Request.Context(), artistID,
		)
		if err != nil {
			h.writeAppError(c, err)
			return
		}
		artist.Stats = stats
	}

	c.JSON(http.StatusOK, artist)
}

// getArtistImportStatus returns the queued/running/done/failed import state for an artist.
func (h *Handler) getArtistImportStatus(c *gin.Context) {
	artistID, err := requireMBID(c.Param("id"))
	if err != nil {
		h.writeAppError(c, err)
		return
	}

	art, err := h.st.GetArtistByID(c.Request.Context(), artistID)
	if err != nil {
		h.writeAppError(c, err)
		return
	}

	// Collaboration-only artists are hidden unless ?all=true.
	if c.Query("all") != "true" && art.InLibrary != 1 {
		h.writeAppError(c, apperrors.ErrNotFound)
		return
	}

	c.JSON(http.StatusOK, h.queue.ArtistImportStatus(artistID))
}

// refreshArtist requeues a discography import for a library artist (202).
func (h *Handler) refreshArtist(c *gin.Context) {
	artistID, err := requireMBID(c.Param("id"))
	if err != nil {
		h.writeAppError(c, err)
		return
	}

	art, err := h.st.GetArtistByID(c.Request.Context(), artistID)
	if err != nil {
		h.writeAppError(c, err)
		return
	}

	// Discography imports require library membership.
	if art.InLibrary != 1 {
		h.writeAppError(c, apperrors.ErrNotFound)
		return
	}

	c.JSON(http.StatusAccepted, h.queue.EnqueueRefreshArtist(artistID))
}

type PostArtistBody struct {
	ID string `json:"id" binding:"required"`
}

// postArtist adds an artist from MusicBrainz by MBID, 201 created, 200 when promoting
// a collaborator, or 304 when already in the library (refresh still requeued).
func (h *Handler) postArtist(c *gin.Context) {
	ctx := c.Request.Context()
	var body PostArtistBody

	if err := c.ShouldBindBodyWith(&body, binding.JSON); err != nil {
		h.writeAppError(c, apperrors.ErrInvalidInput)
		return
	}

	artistID, err := requireMBID(body.ID)
	if err != nil {
		h.writeAppError(c, err)
		return
	}

	// Check DB
	art, err := h.st.GetArtistByID(ctx, artistID)
	if err != nil && !apperrors.Is(err, apperrors.ErrNotFound) {
		h.writeAppError(c, err)
		return
	}

	if art != nil && art.InLibrary == 1 {
		h.queue.EnqueueRefreshArtist(art.ID)
		c.JSON(http.StatusNotModified, nil)
		return
	}
	promoting := art != nil

	// Fetch MusicBrainz
	artist, err := h.mb.GetArtistByID(ctx, artistID)
	if err != nil {
		h.writeAppError(c, err)
		return
	}

	stArtist := tasks.ArtistFromMB(artist)
	if err := h.st.CreateArtist(ctx, stArtist); err != nil {
		h.writeAppError(c, err)
		return
	}

	// Import discography (CreateArtist marks the row in_library = 1).
	h.queue.EnqueueRefreshArtist(stArtist.ID)

	if promoting {
		c.JSON(http.StatusOK, stArtist)
		return
	}

	c.JSON(http.StatusCreated, stArtist)
}

// deleteArtist removes an artist's primary/guest-only groups and returns pruned collaborators.
func (h *Handler) deleteArtist(c *gin.Context) {
	ctx := c.Request.Context()
	artistID, err := requireMBID(c.Param("id"))
	if err != nil {
		h.writeAppError(c, err)
		return
	}

	pruned, err := h.st.DeleteArtist(ctx, artistID)
	if err != nil {
		h.writeAppError(c, err)
		return
	}

	// The artist is gone; its import status entry would linger forever.
	h.queue.ForgetArtistImport(artistID)

	c.JSON(http.StatusOK, map[string]int64{"pruned": pruned})
}
