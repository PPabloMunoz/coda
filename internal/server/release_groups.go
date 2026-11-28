package server

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"

	apperrors "github.com/ppablomunoz/coda/internal/errors"
	"github.com/ppablomunoz/coda/internal/store"
	"github.com/ppablomunoz/coda/internal/tasks"
)

type updateReleaseBody struct {
	NewReleaseID string `json:"new_release_id"`
}

// getReleaseGroups lists release groups with artist/type/has-release/primary filters.
func (h *Handler) getReleaseGroups(c *gin.Context) {
	ctx := c.Request.Context()

	limit, offset, err := parseLimitAndOffset(c)
	if err != nil {
		h.writeAppError(c, err)
		return
	}

	artistID, err := requireOptionalMBID(c.Query("artist"))
	if err != nil {
		h.writeAppError(c, err)
		return
	}

	includeRelease := hasInclude(c, "release")

	groupType := strings.ToLower(strings.TrimSpace(c.Query("type")))
	switch groupType {
	case "", "album", "ep", "single", "other":
	default:
		h.writeAppError(c, apperrors.Wrap(
			apperrors.ErrInvalidInput,
			fmt.Errorf("invalid 'type' parameter"),
		))
		return
	}

	hasRelease, err := parseOptionalBool(c.Query("has_release"))
	if err != nil {
		h.writeAppError(c, err)
		return
	}

	primary, err := parseOptionalBool(c.Query("primary"))
	if err != nil {
		h.writeAppError(c, err)
		return
	}
	if primary != nil && artistID == "" {
		h.writeAppError(c, apperrors.Wrap(
			apperrors.ErrInvalidInput,
			fmt.Errorf("primary requires artist"),
		))
		return
	}

	groups, total, err := h.st.ListReleaseGroups(
		ctx, store.ReleaseGroupListOptions{
			SearchOptions:  store.SearchOptions{Query: c.Query("q")},
			SortOptions:    store.SortOptions{Sort: c.Query("sort"), Order: c.Query("order")},
			PageOptions:    store.PageOptions{Limit: limit, Offset: offset},
			ArtistID:       artistID,
			IncludeRelease: includeRelease,
			Type:           groupType,
			HasRelease:     hasRelease,
			Primary:        primary,
		})
	if err != nil {
		h.writeAppError(c, err)
		return
	}

	response := Response[[]store.ReleaseGroupParams]{
		Total: total,
		Items: groups,
	}

	c.JSON(http.StatusOK, response)
}

// getReleaseGroup returns one release group by MBID.
func (h *Handler) getReleaseGroup(c *gin.Context) {
	ctx := c.Request.Context()

	releaseGroupID, err := requireMBID(c.Param("id"))
	if err != nil {
		h.writeAppError(c, err)
		return
	}

	group, err := h.st.GetReleaseGroup(
		ctx, releaseGroupID,
	)
	if err != nil {
		h.writeAppError(c, err)
		return
	}

	c.JSON(http.StatusOK, group)
}

// replaceRelease swaps a group's kept release for another official release in the same group.
func (h *Handler) replaceRelease(c *gin.Context) {
	ctx := c.Request.Context()
	releaseGroupID, err := requireMBID(c.Param("id"))
	if err != nil {
		h.writeAppError(c, err)
		return
	}

	var body updateReleaseBody
	if err := c.ShouldBindBodyWith(&body, binding.JSON); err != nil {
		h.writeAppError(c, apperrors.Wrap(
			apperrors.ErrInvalidInput,
			fmt.Errorf("body invalid: %w", err)),
		)
		return
	}

	newReleaseID, err := requireMBID(body.NewReleaseID)
	if err != nil {
		h.writeAppError(c, err)
		return
	}

	newRelease, err := h.mb.GetReleaseByID(
		ctx, releaseGroupID, newReleaseID,
	)
	if err != nil {
		if apperrors.Is(err, apperrors.ErrNotFound) ||
			apperrors.Is(err, apperrors.ErrInvalidInput) {
			h.writeAppError(c, err)
			return
		}
		h.writeAppError(c, apperrors.Wrap(
			apperrors.ErrMusicBrainzFailed,
			fmt.Errorf("switch release: %w", err),
		))
		return
	}

	stRelease := tasks.FullReleaseFromMB(*newRelease)
	// Capture the outgoing release so its cached files can be dropped below.
	// Best-effort: a lookup miss just skips cleanup, the DB row cascades.
	oldRelease, oldErr := h.st.GetReleaseOfGroup(ctx, releaseGroupID, false)
	if err := h.st.ReplaceRelease(ctx,
		releaseGroupID, stRelease); err != nil {
		h.writeAppError(c, err)
		return
	}
	if oldErr == nil && oldRelease.ID != newReleaseID {
		_ = h.st.DeleteCover(ctx, oldRelease.ID)
		h.removeCoverFiles(oldRelease.ID)
	}
	// The DB link is already remapped; heal tags, covers, and paths in the
	// background so the switch stays fast.
	h.queue.EnqueueRelinkRelease(newReleaseID)

	c.JSON(http.StatusOK, nil)
}

// deleteReleaseGroup deletes a release group and its cached cover files.
// ?permanent=true also tombstones the group so later discography imports
// skip it until the tombstone is removed from /ignored.
func (h *Handler) deleteReleaseGroup(c *gin.Context) {
	ctx := c.Request.Context()
	releaseGroupID, err := requireMBID(c.Param("id"))
	if err != nil {
		h.writeAppError(c, err)
		return
	}

	permanent, err := parseOptionalBool(c.Query("permanent"))
	if err != nil {
		h.writeAppError(c, apperrors.Wrap(
			apperrors.ErrInvalidInput,
			fmt.Errorf("invalid 'permanent' parameter: must be a boolean"),
		))
		return
	}

	// Capture the kept release so its cached files can be dropped below.
	// Best-effort: a lookup miss just skips cleanup, DB rows cascade.
	oldRelease, oldErr := h.st.GetReleaseOfGroup(ctx, releaseGroupID, false)
	if err := h.st.DeleteReleaseGroup(ctx, releaseGroupID, permanent != nil && *permanent); err != nil {
		h.writeAppError(c, err)
		return
	}
	if oldErr == nil {
		_ = h.st.DeleteCover(ctx, oldRelease.ID)
		h.removeCoverFiles(oldRelease.ID)
	}

	c.JSON(http.StatusOK, nil)
}
