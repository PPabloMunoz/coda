package server

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/ppablomunoz/coda/internal/artwork"
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

// uploadReleaseCover replaces the cached cover for a release with
// caller-supplied image bytes and queues a background re-embed into the
// release's linked files. The body is the raw image (Content-Type is only
// a hint); the real type is sniffed from magic bytes. Refresh keeps its
// meaning (fetch provider art) and replaces an upload.
func (h *Handler) uploadReleaseCover(c *gin.Context) {
	ctx := c.Request.Context()

	releaseID, err := requireMBID(c.Param("id"))
	if err != nil {
		h.writeAppError(c, err)
		return
	}
	if _, err := h.st.GetRelease(ctx, releaseID, false); err != nil {
		h.writeAppError(c, err)
		return
	}

	img, mime, err := readCoverUpload(c)
	if err != nil {
		h.writeAppError(c, err)
		return
	}
	if err := tasks.SaveCover(ctx, h.st, h.coverDir, releaseID, &artwork.Result{
		Bytes:  img,
		MIME:   mime,
		Source: artwork.SourceUpload,
	}); err != nil {
		h.writeAppError(c, err)
		return
	}

	row, err := h.st.GetCover(ctx, releaseID)
	if err != nil {
		h.writeAppError(c, err)
		return
	}
	h.queue.EnqueueEmbedReleaseCover(releaseID)
	c.JSON(http.StatusOK, row)
}

// uploadArtistCover replaces the cached artist photo with caller-supplied
// image bytes. Artist photos are display-only: they are never embedded
// into audio files, so no background work is queued.
func (h *Handler) uploadArtistCover(c *gin.Context) {
	ctx := c.Request.Context()

	artistID, err := requireMBID(c.Param("id"))
	if err != nil {
		h.writeAppError(c, err)
		return
	}
	if _, err := h.st.GetArtistByID(ctx, artistID); err != nil {
		h.writeAppError(c, err)
		return
	}

	img, mime, err := readCoverUpload(c)
	if err != nil {
		h.writeAppError(c, err)
		return
	}
	if err := tasks.SaveArtistCover(ctx, h.st, h.coverDir, artistID, &artwork.Result{
		Bytes:  img,
		MIME:   mime,
		Source: artwork.SourceUpload,
	}); err != nil {
		h.writeAppError(c, err)
		return
	}

	row, err := h.st.GetArtistCover(ctx, artistID)
	if err != nil {
		h.writeAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, row)
}

// deleteReleaseCover removes the cached cover file for a release and records
// a tombstone so later GETs stay 404 instead of lazy-fetching provider art
// again. Idempotent: removing an already-removed or never-cached cover
// still answers 204. A background task clears the embedded front cover
// from the release's linked files. Refresh fetches provider art again and
// clears the removal, as does a new upload.
func (h *Handler) deleteReleaseCover(c *gin.Context) {
	ctx := c.Request.Context()

	releaseID, err := requireMBID(c.Param("id"))
	if err != nil {
		h.writeAppError(c, err)
		return
	}
	if _, err := h.st.GetRelease(ctx, releaseID, false); err != nil {
		h.writeAppError(c, err)
		return
	}

	if err := h.st.MarkCoverRemoved(ctx, releaseID); err != nil {
		h.writeAppError(c, err)
		return
	}
	removeCoverFiles(h.coverDir, releaseID)
	h.queue.EnqueueClearReleaseCover(releaseID)
	c.Status(http.StatusNoContent)
}

// deleteArtistCover removes the cached artist photo and records a tombstone
// so later GETs stay 404 instead of lazy-fetching a provider photo again.
// Idempotent: removing an already-removed or never-cached photo still
// answers 204. Artist photos are display-only, so no background work is
// queued. Refresh fetches a provider photo again and clears the removal,
// as does a new upload.
func (h *Handler) deleteArtistCover(c *gin.Context) {
	ctx := c.Request.Context()

	artistID, err := requireMBID(c.Param("id"))
	if err != nil {
		h.writeAppError(c, err)
		return
	}
	if _, err := h.st.GetArtistByID(ctx, artistID); err != nil {
		h.writeAppError(c, err)
		return
	}

	if err := h.st.MarkArtistCoverRemoved(ctx, artistID); err != nil {
		h.writeAppError(c, err)
		return
	}
	removeArtistCoverFiles(h.coverDir, artistID)
	c.Status(http.StatusNoContent)
}

// readCoverUpload reads a raw image upload body with the same size cap as
// the provider fetcher and sniffs the real content type from magic bytes.
// Only JPEG, PNG, and WebP are accepted.
func readCoverUpload(c *gin.Context) ([]byte, string, error) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, artwork.DefaultMaxBytes+1)
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		if strings.Contains(err.Error(), "request body too large") {
			return nil, "", apperrors.Wrap(
				apperrors.ErrInvalidInput,
				fmt.Errorf("cover image exceeds %dMB limit", artwork.DefaultMaxBytes>>20),
			)
		}
		return nil, "", apperrors.Wrap(
			apperrors.ErrInvalidInput,
			fmt.Errorf("read cover image: %w", err),
		)
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, "", apperrors.Wrap(
			apperrors.ErrInvalidInput,
			fmt.Errorf("cover image is empty"),
		)
	}
	if int64(len(body)) > artwork.DefaultMaxBytes {
		return nil, "", apperrors.Wrap(
			apperrors.ErrInvalidInput,
			fmt.Errorf("cover image exceeds %dMB limit", artwork.DefaultMaxBytes>>20),
		)
	}
	mime := http.DetectContentType(body)
	switch mime {
	case "image/jpeg", "image/png", "image/webp":
		return body, mime, nil
	default:
		return nil, "", apperrors.Wrap(
			apperrors.ErrInvalidInput,
			fmt.Errorf("unsupported cover image type %q (want JPEG, PNG, or WebP)", mime),
		)
	}
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
