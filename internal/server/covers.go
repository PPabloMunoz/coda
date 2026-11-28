package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"

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

// serveCover ensures and serves the cached cover bytes for a release with
// ETag revalidation. The endpoint answers no-cache (store, but revalidate
// every use) so uploads and refreshes show up without a hard reload; the
// ETag validator rides on 200 and 304 alike, and even misses carry
// no-cache so a removed cover never sticks in the browser.
func (h *Handler) serveCover(c *gin.Context, releaseID string) {
	c.Header("Cache-Control", "no-cache")
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

	etag := `"` + strconv.FormatInt(row.SizeBytes, 10) + "-" + row.FetchedAt + `"`
	c.Header("ETag", etag)
	if c.GetHeader("If-None-Match") == etag {
		c.Status(http.StatusNotModified)
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
		// so the next request retries instead of 500ing forever. Only drop
		// the row we served: a concurrent upload, refresh, or delete may
		// have replaced it or recorded a tombstone while we were reading.
		if cur, gerr := h.st.GetCover(ctx, releaseID); gerr == nil && !cur.Removed &&
			cur.MIME == row.MIME && cur.SizeBytes == row.SizeBytes && cur.FetchedAt == row.FetchedAt {
			_ = h.st.DeleteCover(ctx, releaseID)
		} else if gerr == nil && cur.Removed {
			h.writeAppError(c, apperrors.ErrNotFound)
			return
		} else if apperrors.Is(gerr, apperrors.ErrNotFound) {
			h.writeAppError(c, apperrors.ErrNotFound)
			return
		}
		h.writeAppError(c, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("cover file=%q read -> %w", releaseID, err)),
		)
		return
	}

	c.Data(http.StatusOK, row.MIME, data)
}

// refreshReleaseCover fetches provider art again and swaps the cache only
// on success; a fetch failure or a no-art miss leaves the previous cached
// art untouched. Missing art answers 404 null; an upstream failure
// surfaces as an error so the old state is never confused with "no art".
// A successful refresh queues the same background re-embed into linked
// files that uploads use.
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

	h.queue.EnqueueEmbedReleaseCover(releaseID)
	c.JSON(http.StatusOK, row)
}

// doRefreshReleaseCover fetches provider art for a release and swaps the
// cached bytes/row only on success.
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

	artist := tasks.CreditDisplayName(rel.Credit)
	if artist == "" {
		artist = tasks.CreditDisplayName(rel.Group.Credit)
	}
	return tasks.RefreshCover(ctx, h.st, h.art, h.coverDir, releaseID, artist, rel.Title)
}

// getReleaseCoverMeta returns the cached cover metadata row for a release
// (source, mime, size, fetched_at) without fetching anything. Unknown
// releases, missing art, and user-removed covers answer 404 null — the web
// client treats 404 as null, same as other entities.
func (h *Handler) getReleaseCoverMeta(c *gin.Context) {
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
	row, err := h.st.GetCover(ctx, releaseID)
	if err != nil {
		h.writeAppError(c, err)
		return
	}
	if row.Removed {
		h.writeAppError(c, apperrors.ErrNotFound)
		return
	}
	// Never store the metadata row: upload/refresh responses must not feed
	// a stale badge on the next read.
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, row)
}

// saveReleaseCoverBytes stores res under the release cover lock and returns
// the fresh metadata row. Raw file uploads and URL imports share it (lock,
// atomic write, tombstone clear); callers queue the re-embed.
func (h *Handler) saveReleaseCoverBytes(ctx context.Context, releaseID string, res *artwork.Result) (*store.CoverParams, error) {
	var row *store.CoverParams
	if err := tasks.WithReleaseCoverLock(releaseID, func() error {
		if err := tasks.SaveCover(ctx, h.st, h.coverDir, releaseID, res); err != nil {
			return err
		}
		r, err := h.st.GetCover(ctx, releaseID)
		if err != nil {
			return err
		}
		row = r
		return nil
	}); err != nil {
		return nil, err
	}
	return row, nil
}

// saveArtistCoverBytes stores res under the artist cover lock and returns
// the fresh metadata row. Raw file uploads and URL imports share it.
func (h *Handler) saveArtistCoverBytes(ctx context.Context, artistID string, res *artwork.Result) (*store.ArtistCoverParams, error) {
	var row *store.ArtistCoverParams
	if err := tasks.WithArtistCoverLock(artistID, func() error {
		if err := tasks.SaveArtistCover(ctx, h.st, h.coverDir, artistID, res); err != nil {
			return err
		}
		r, err := h.st.GetArtistCover(ctx, artistID)
		if err != nil {
			return err
		}
		row = r
		return nil
	}); err != nil {
		return nil, err
	}
	return row, nil
}

type coverURLBody struct {
	URL string `json:"url" binding:"required"`
}

// requireCoverURL parses the {"url": ...} body of the cover-URL endpoints.
// The bounds mirror the fetcher cap (2 KiB) so an overlong URL fails fast
// here instead of after validation downstream.
func requireCoverURL(c *gin.Context) (string, error) {
	var body coverURLBody
	if err := c.ShouldBindBodyWith(&body, binding.JSON); err != nil {
		return "", apperrors.Wrap(
			apperrors.ErrInvalidInput,
			fmt.Errorf("body invalid: %w", err),
		)
	}
	raw := strings.TrimSpace(body.URL)
	if raw == "" || len(raw) > artwork.MaxCoverURLLen {
		return "", apperrors.Wrap(
			apperrors.ErrInvalidInput,
			fmt.Errorf("cover url invalid"),
		)
	}
	return raw, nil
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
	row, err := h.saveReleaseCoverBytes(ctx, releaseID, &artwork.Result{
		Bytes:  img,
		MIME:   mime,
		Source: artwork.SourceUpload,
	})
	if err != nil {
		h.writeAppError(c, err)
		return
	}
	h.queue.EnqueueEmbedReleaseCover(releaseID)
	c.JSON(http.StatusOK, row)
}

// uploadReleaseCoverURL fetches a user-supplied image URL server-side and
// caches it as the release cover, recording the URL as the row's source.
// The browser never fetches the image itself (most hosts lack CORS
// headers). Like a raw upload it queues a background re-embed into the
// release's linked files.
func (h *Handler) uploadReleaseCoverURL(c *gin.Context) {
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
	raw, err := requireCoverURL(c)
	if err != nil {
		h.writeAppError(c, err)
		return
	}
	if h.art == nil {
		h.writeAppError(c, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("cover URL import unavailable"),
		))
		return
	}
	res, err := h.art.FetchURL(ctx, raw)
	if err != nil {
		h.writeAppError(c, err)
		return
	}
	row, err := h.saveReleaseCoverBytes(ctx, releaseID, res)
	if err != nil {
		h.writeAppError(c, err)
		return
	}
	h.queue.EnqueueEmbedReleaseCover(releaseID)
	c.JSON(http.StatusOK, row)
}

// getArtistCoverMeta returns the cached artist photo metadata row without
// fetching anything. Unknown artists, missing photos, and user-removed
// photos answer 404 null — the web client treats 404 as null.
func (h *Handler) getArtistCoverMeta(c *gin.Context) {
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
	row, err := h.st.GetArtistCover(ctx, artistID)
	if err != nil {
		h.writeAppError(c, err)
		return
	}
	if row.Removed {
		h.writeAppError(c, apperrors.ErrNotFound)
		return
	}
	// Never store the metadata row: upload/refresh responses must not feed
	// a stale badge on the next read.
	c.Header("Cache-Control", "no-store")
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
	row, err := h.saveArtistCoverBytes(ctx, artistID, &artwork.Result{
		Bytes:  img,
		MIME:   mime,
		Source: artwork.SourceUpload,
	})
	if err != nil {
		h.writeAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, row)
}

// uploadArtistCoverURL fetches a user-supplied image URL server-side and
// caches it as the artist photo, recording the URL as the row's source.
// The browser never fetches the image itself (most hosts lack CORS
// headers). Artist photos are display-only, so no background work is
// queued.
func (h *Handler) uploadArtistCoverURL(c *gin.Context) {
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
	raw, err := requireCoverURL(c)
	if err != nil {
		h.writeAppError(c, err)
		return
	}
	if h.art == nil {
		h.writeAppError(c, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("cover URL import unavailable"),
		))
		return
	}
	res, err := h.art.FetchURL(ctx, raw)
	if err != nil {
		h.writeAppError(c, err)
		return
	}
	row, err := h.saveArtistCoverBytes(ctx, artistID, res)
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

	if err := tasks.WithReleaseCoverLock(releaseID, func() error {
		if err := h.st.MarkCoverRemoved(ctx, releaseID); err != nil {
			return err
		}
		removeCoverFiles(h.coverDir, releaseID)
		return nil
	}); err != nil {
		h.writeAppError(c, err)
		return
	}
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

	if err := tasks.WithArtistCoverLock(artistID, func() error {
		if err := h.st.MarkArtistCoverRemoved(ctx, artistID); err != nil {
			return err
		}
		removeArtistCoverFiles(h.coverDir, artistID)
		return nil
	}); err != nil {
		h.writeAppError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// readCoverUpload reads a raw image upload body with the same size cap as
// the provider fetcher and sniffs the real content type from magic bytes.
// Only JPEG, PNG, and WebP are accepted.
func readCoverUpload(c *gin.Context) ([]byte, string, error) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, artwork.DefaultMaxBytes+1)
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
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

// serveArtistCover ensures and serves the cached photo bytes for an artist
// with ETag revalidation. The endpoint answers no-cache (store, but
// revalidate every use) so uploads and refreshes show up without a hard
// reload; the ETag validator rides on 200 and 304 alike, and even misses
// carry no-cache so a removed photo never sticks in the browser.
func (h *Handler) serveArtistCover(c *gin.Context, artistID string) {
	c.Header("Cache-Control", "no-cache")
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

	etag := `"` + strconv.FormatInt(row.SizeBytes, 10) + "-" + row.FetchedAt + `"`
	c.Header("ETag", etag)
	if c.GetHeader("If-None-Match") == etag {
		c.Status(http.StatusNotModified)
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
		// so the next request retries instead of 500ing forever. Only drop
		// the row we served: a concurrent upload, refresh, or delete may
		// have replaced it or recorded a tombstone while we were reading.
		if cur, gerr := h.st.GetArtistCover(ctx, artistID); gerr == nil && !cur.Removed &&
			cur.MIME == row.MIME && cur.SizeBytes == row.SizeBytes && cur.FetchedAt == row.FetchedAt {
			_ = h.st.DeleteArtistCover(ctx, artistID)
		} else if gerr == nil && cur.Removed {
			h.writeAppError(c, apperrors.ErrNotFound)
			return
		} else if apperrors.Is(gerr, apperrors.ErrNotFound) {
			h.writeAppError(c, apperrors.ErrNotFound)
			return
		}
		h.writeAppError(c, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("artist cover artist=%q read -> %w", artistID, err)),
		)
		return
	}

	c.Data(http.StatusOK, row.MIME, data)
}

// refreshArtistCover fetches the photo again (Deezer, then Wikimedia) and
// swaps the cache only on success; a fetch failure or a no-photo miss
// leaves the previous cached photo untouched. Missing art answers 404
// null; an upstream failure surfaces as an error so the old state is never
// confused with "no art".
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

	row, err := tasks.RefreshArtistCover(ctx, h.st, h.art, h.coverDir, artistID, artist.Name)
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
