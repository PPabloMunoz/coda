package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/ppablomunoz/coda/internal/config"
	apperrors "github.com/ppablomunoz/coda/internal/errors"
	"github.com/ppablomunoz/coda/internal/store"
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

// serveCover ensures and serves the cached cover bytes for a release with ETag caching.
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

// refreshReleaseCover drops the cached cover for a release and fetches it
// again (CAA, then Deezer). Missing art answers 404 null; an upstream
// failure surfaces as an error so the old state is never confused with "no
// art". The row cascades are untouched — only the cache is rebuilt.
func (h *Handler) refreshReleaseCover(c *gin.Context) {
	ctx := c.Request.Context()

	releaseID, err := requireMBID(c.Param("id"))
	if err != nil {
		h.writeAppError(c, err)
		return
	}

	row, err := h.doRefreshReleaseCover(ctx, releaseID)
	if err != nil {
		h.writeAppError(c, err)
		return
	}

	c.JSON(http.StatusOK, row)
}

// doRefreshReleaseCover drops cached bytes/row for a release and refetches its cover.
func (h *Handler) doRefreshReleaseCover(ctx context.Context, releaseID string) (*store.CoverParams, error) {
	rel, err := h.st.GetRelease(ctx, releaseID, false)
	if err != nil {
		return nil, err
	}
	if h.art == nil {
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("cover refresh unavailable"),
		)
	}

	_ = h.st.DeleteCover(ctx, releaseID)
	removeCoverFiles(h.coverDir, releaseID)

	artist := tasks.CreditDisplayName(rel.Credit)
	if artist == "" {
		artist = tasks.CreditDisplayName(rel.Group.Credit)
	}
	return tasks.EnsureCover(ctx, h.st, h.art, h.coverDir, releaseID, artist, rel.Title)
}

// embedCover stamps the cached release art into the audio file as its front
// cover. Single owner is tasks.EmbedCover (reused by the scan auto-link).
func (h *Handler) embedCover(ctx context.Context, filePath, releaseID string) error {
	return tasks.EmbedCover(ctx, h.st, h.coverDir, filePath, releaseID)
}

// removeCoverFiles deletes every cached file for a release (all extensions).
// The DB row is expected to be gone already (FK cascade or explicit delete).
func removeCoverFiles(coverDir, releaseID string) {
	matches, _ := filepath.Glob(filepath.Join(coverDir, releaseID+".*"))
	for _, m := range matches {
		_ = os.Remove(m)
	}
}

// getArtistCover serves the cached photo for an artist, fetching it on a
// cache miss (the import usually prefetched it). Missing art answers 404
// null — the web client treats 404 as null, same as other entities.
// Display-only: artist photos are never embedded into audio files.
func (h *Handler) getArtistCover(c *gin.Context) {
	artistID, err := requireMBID(c.Param("id"))
	if err != nil {
		h.writeAppError(c, err)
		return
	}
	h.serveArtistCover(c, artistID)
}

// serveArtistCover ensures and serves the cached photo bytes for an artist with ETag caching.
func (h *Handler) serveArtistCover(c *gin.Context, artistID string) {
	ctx := c.Request.Context()

	artist, err := h.st.GetArtistByID(ctx, artistID)
	if err != nil {
		h.writeAppError(c, err)
		return
	}

	row, err := tasks.EnsureArtistCover(ctx, h.st, h.art, h.coverDir, artistID, artist.Name)
	if err != nil {
		h.writeAppError(c, err)
		return
	}

	path, err := config.ArtistCoverPath(h.coverDir, artistID, row.MIME)
	if err != nil {
		h.writeAppError(c, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("artist cover path: %w", err)),
		)
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		// Row without bytes (deleted/moved data dir): drop the stale row
		// so the next request retries instead of 500ing forever.
		_ = h.st.DeleteArtistCover(ctx, artistID)
		h.writeAppError(c, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("artist cover artist=%q read -> %w", artistID, err)),
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

// refreshArtistCover drops the cached photo for an artist and fetches it
// again (Deezer, then Wikimedia). Missing art answers 404 null; an upstream
// failure surfaces as an error so the old state is never confused with "no
// art".
func (h *Handler) refreshArtistCover(c *gin.Context) {
	ctx := c.Request.Context()

	artistID, err := requireMBID(c.Param("id"))
	if err != nil {
		h.writeAppError(c, err)
		return
	}

	artist, err := h.st.GetArtistByID(ctx, artistID)
	if err != nil {
		h.writeAppError(c, err)
		return
	}
	if h.art == nil {
		h.writeAppError(c, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("artist cover refresh unavailable"),
		))
		return
	}

	_ = h.st.DeleteArtistCover(ctx, artistID)
	removeArtistCoverFiles(h.coverDir, artistID)

	row, err := tasks.EnsureArtistCover(ctx, h.st, h.art, h.coverDir, artistID, artist.Name)
	if err != nil {
		h.writeAppError(c, err)
		return
	}

	c.JSON(http.StatusOK, row)
}

// removeArtistCoverFiles deletes every cached file for an artist (all
// extensions). Artist files carry the "artist-" prefix, so release files are
// never touched.
func removeArtistCoverFiles(coverDir, artistID string) {
	matches, _ := filepath.Glob(filepath.Join(coverDir, "artist-"+artistID+".*"))
	for _, m := range matches {
		_ = os.Remove(m)
	}
}
