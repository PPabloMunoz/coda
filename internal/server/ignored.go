package server

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	apperrors "github.com/ppablomunoz/coda/internal/errors"
	"github.com/ppablomunoz/coda/internal/store"
)

// getIgnored lists permanently deleted artists and release groups.
func (h *Handler) getIgnored(c *gin.Context) {
	limit, offset, err := parseLimitAndOffset(c)
	if err != nil {
		h.writeAppError(c, err)
		return
	}

	typ := strings.ToLower(strings.TrimSpace(c.Query("type")))
	switch typ {
	case "", "artist", "release_group":
	default:
		h.writeAppError(c, apperrors.Wrap(
			apperrors.ErrInvalidInput,
			fmt.Errorf("invalid 'type' parameter"),
		))
		return
	}

	items, total, err := h.st.ListIgnored(
		c.Request.Context(), store.IgnoredListOptions{
			SearchOptions: store.SearchOptions{Query: c.Query("q")},
			SortOptions:   store.SortOptions{Sort: c.Query("sort"), Order: c.Query("order")},
			PageOptions:   store.PageOptions{Limit: limit, Offset: offset},
			Type:          typ,
		})
	if err != nil {
		h.writeAppError(c, err)
		return
	}

	response := Response[[]store.IgnoredEntry]{
		Total: total,
		Items: items,
	}

	c.JSON(http.StatusOK, response)
}

// deleteIgnored removes a tombstone so the entity can be added again.
// Removing an artist tombstone also reactivates a surviving soft-deleted
// row as a collaborator, so its page can offer Add to library; membership
// still requires POST /artists. Nothing refreshes eagerly: a group returns
// on the next artist refresh, an artist import starts on re-add.
func (h *Handler) deleteIgnored(c *gin.Context) {
	id, err := requireMBID(c.Param("id"))
	if err != nil {
		h.writeAppError(c, err)
		return
	}

	if err := h.st.RemoveIgnored(c.Request.Context(), id); err != nil {
		h.writeAppError(c, err)
		return
	}

	c.Status(http.StatusNoContent)
}
