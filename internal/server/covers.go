package server

import (
	"fmt"
	"net/http"
	"os"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/ppablomunoz/coda/internal/config"
	apperrors "github.com/ppablomunoz/coda/internal/errors"
	"github.com/ppablomunoz/coda/internal/tasks"
)

// getReleaseCover serves the cached front cover for a release, fetching it
// on a cache miss (prefetch usually got there first). Missing art answers
// 404 null — the web client treats 404 as null, same as other entities.
func (h *Handler) getReleaseCover(c *gin.Context) {
	releaseID, err := requireMBID(c.Param("id"))
	if err != nil {
		h.writeAppError(c, err)
		return
	}
	h.serveCover(c, releaseID)
}

// getReleaseGroupCover serves the cover of the single release kept for a
// release-group. One release per group is enforced by the UNIQUE on
// releases.release_group_id.
func (h *Handler) getReleaseGroupCover(c *gin.Context) {
	ctx := c.Request.Context()

	groupID, err := requireMBID(c.Param("id"))
	if err != nil {
		h.writeAppError(c, err)
		return
	}

	release, err := h.st.GetReleaseOfGroup(ctx, groupID, false)
	if err != nil {
		h.writeAppError(c, err)
		return
	}
	h.serveCover(c, release.ID)
}

func (h *Handler) serveCover(c *gin.Context, releaseID string) {
	ctx := c.Request.Context()

	rel, err := h.st.GetRelease(ctx, releaseID, false)
	if err != nil {
		h.writeAppError(c, err)
		return
	}

	artist := tasks.CreditDisplayName(rel.Credit)
	if artist == "" {
		artist = tasks.CreditDisplayName(rel.Group.Credit)
	}
	row, err := tasks.EnsureCover(ctx, h.st, h.art, h.coverDir, releaseID, artist, rel.Title)
	if err != nil {
		h.writeAppError(c, err)
		return
	}

	path, err := config.CoverPath(h.coverDir, releaseID, row.MIME)
	if err != nil {
		h.writeAppError(c, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("cover path: %w", err)),
		)
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		// Row without bytes (deleted/moved data dir): drop the stale row
		// so the next request retries instead of 500ing forever.
		_ = h.st.DeleteCover(ctx, releaseID)
		h.writeAppError(c, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("cover file=%q read -> %w", releaseID, err)),
		)
		return
	}

	etag := `"` + strconv.FormatInt(row.SizeBytes, 10) + "-" + row.FetchedAt + `"`
	if c.GetHeader("If-None-Match") == etag {
		c.Status(http.StatusNotModified)
		return
	}
	c.Header("ETag", etag)
	c.Header("Cache-Control", "public, max-age=86400")
	c.Data(http.StatusOK, row.MIME, data)
}
