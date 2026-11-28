package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

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

	// Lightweight id lookup: serveCover's fast path serves cached bytes
	// without loading the release, so a full GetReleaseOfGroup here would
	// defeat it on every image request.
	releaseID, err := h.st.GetReleaseIDOfGroup(ctx, groupID)
	if err != nil {
		h.writeAppError(c, err)
		return
	}
	h.serveCover(c, releaseID)
}

// coverETag builds the validator for a cached cover row.
func coverETag(sizeBytes int64, fetchedAt string) string {
	return `"` + strconv.FormatInt(sizeBytes, 10) + "-" + fetchedAt + `"`
}

// serveCoverFile streams a cached cover file from disk with Range support.
// The row's ETag/If-None-Match check must already have run: this only
// serves the 200/206 body. It reports whether the file was served; an
// unserved file means the caller should run its stale-row recovery
// (missing dir, missing file, non-regular entry), and the returned error
// says why the bytes could not be opened. The open is scoped to the cover
// dir via os.Root so a symlink swapped mid-request cannot redirect the
// read outside the cache.
func serveCoverFile(c *gin.Context, coverDir, fullPath, mime string) (bool, error) {
	if coverDir == "" {
		return false, nil
	}
	_, base := filepath.Split(fullPath)
	if base == "" || base == "." || base == "/" {
		return false, fmt.Errorf("cover path %q has no file name", fullPath)
	}
	ro, err := os.OpenRoot(coverDir)
	if err != nil {
		return false, fmt.Errorf("open cover dir %q: %w", coverDir, err)
	}
	defer func() { _ = ro.Close() }()
	f, err := ro.Open(base)
	if err != nil {
		return false, fmt.Errorf("open cached cover %q: %w", fullPath, err)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return false, fmt.Errorf("stat cached cover %q: %w", fullPath, err)
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("cached cover %q is not a regular file", fullPath)
	}
	c.Header("Content-Type", mime)
	http.ServeContent(c.Writer, c.Request, "", time.Time{}, f)
	return true, nil
}

// logCoverFileError keeps a missing cache file quiet (normal stale-row
// recovery) and everything else visible: permissions, a directory where a
// file should be, or a broken data dir.
func (h *Handler) logCoverFileError(kind, entityID, path string, err error) {
	if err == nil {
		return
	}
	if errors.Is(err, fs.ErrNotExist) {
		h.log.Debugf("%s %q cached file %q missing", kind, entityID, path)
		return
	}
	h.log.Warnf("%s %q cached file %q: %s", kind, entityID, path, apperrors.LogDetail(err))
}

// dropStaleCoverRow removes a cache row whose bytes are gone so the next
// request refetches instead of serving a 500 forever. It only logs: the
// caller continues with its miss path either way.
func (h *Handler) dropStaleCoverRow(kind, entityID string, drop func() error) {
	if err := drop(); err != nil {
		h.log.Warnf("%s %q: drop stale cache row: %s", kind, entityID, apperrors.LogDetail(err))
		return
	}
	h.log.Infof("%s %q: dropped stale cache row", kind, entityID)
}

// serveCover ensures and serves the cached cover bytes for a release with
// ETag revalidation. The endpoint answers no-cache (store, but revalidate
// every use) so uploads and refreshes show up without a hard reload; the
// ETag validator rides on 200 and 304 alike, and even misses carry
// no-cache so a removed cover never sticks in the browser. Bodies stream
// from disk (Range-capable) instead of loading into memory.
//
// Fast path: a live cached row serves 304/200 without loading the release
// (a cover row implies its release exists via FK cascade). The release is
// only loaded on a cache miss or stale bytes, when artist/title are needed
// to fetch providers.
func (h *Handler) serveCover(c *gin.Context, releaseID string) {
	c.Header("Cache-Control", "no-cache")
	ctx := c.Request.Context()

	if row, err := h.st.GetCover(ctx, releaseID); err == nil {
		if row.Removed {
			h.writeAppError(c, apperrors.ErrNotFound)
			return
		}
		etag := coverETag(row.SizeBytes, row.FetchedAt)
		c.Header("ETag", etag)
		if c.GetHeader("If-None-Match") == etag {
			c.Status(http.StatusNotModified)
			return
		}
		if path, perr := config.CoverPath(h.coverDir, releaseID, row.MIME); perr != nil {
			h.log.Warnf("cover cache release %q path: %s", releaseID, apperrors.LogDetail(perr))
		} else if served, serr := serveCoverFile(c, h.coverDir, path, row.MIME); served {
			return
		} else {
			h.logCoverFileError("cover cache release", releaseID, path, serr)
		}
		// Cached row without readable bytes (deleted/moved data dir):
		// drop the stale row so the slow path below refetches instead
		// of 500ing. Only drop the row we checked: a concurrent
		// upload, refresh, or delete may have replaced it or recorded
		// a tombstone while we were reading.
		if cur, gerr := h.st.GetCover(ctx, releaseID); gerr == nil && !cur.Removed &&
			cur.MIME == row.MIME && cur.SizeBytes == row.SizeBytes && cur.FetchedAt == row.FetchedAt {
			h.dropStaleCoverRow("cover cache release", releaseID, func() error {
				return h.st.DeleteCover(ctx, releaseID)
			})
		} else if gerr == nil && cur.Removed {
			h.writeAppError(c, apperrors.ErrNotFound)
			return
		} else if gerr != nil && !apperrors.Is(gerr, apperrors.ErrNotFound) {
			h.writeAppError(c, fmt.Errorf("cover cache release=%q recheck: %w", releaseID, gerr))
			return
		}
	} else if apperrors.Is(err, apperrors.ErrNotFound) {
		// No cached row: an active negative-cache entry is a 404
		// without loading the release or touching providers.
		if m, merr := h.st.GetReleaseCoverMiss(ctx, releaseID); merr == nil {
			if m.Active(time.Now()) {
				h.writeAppError(c, apperrors.ErrNotFound)
				return
			}
		} else if !apperrors.Is(merr, apperrors.ErrNotFound) {
			h.writeAppError(c, merr)
			return
		}
	} else {
		h.writeAppError(c, err)
		return
	}

	rel, err := h.st.GetRelease(ctx, releaseID, false)
	if err != nil {
		h.writeAppError(c, err)
		return
	}

	artist := tasks.CreditDisplayName(rel.Credit)
	if artist == "" {
		artist = tasks.CreditDisplayName(rel.Group.Credit)
	}
	row, fetched, err := tasks.EnsureCover(ctx, h.st, h.art, h.coverDir, releaseID, artist, rel.Title)
	if err != nil {
		h.writeAppError(c, err)
		return
	}
	if fetched {
		// Fresh provider art for an already-linked release: queue the
		// same background re-embed uploads and refreshes use, so the
		// new cover reaches the audio files without manual action.
		h.queue.EnqueueEmbedReleaseCover(releaseID)
	}

	etag := coverETag(row.SizeBytes, row.FetchedAt)
	c.Header("ETag", etag)
	if c.GetHeader("If-None-Match") == etag {
		c.Status(http.StatusNotModified)
		return
	}

	path, err := config.CoverPath(h.coverDir, releaseID, row.MIME)
	if err != nil {
		h.writeAppError(c, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("cover path release=%q: %w", releaseID, err)),
		)
		return
	}
	if served, serr := serveCoverFile(c, h.coverDir, path, row.MIME); served {
		return
	} else {
		h.log.Warnf("cover cache release %q: just-saved file %q unreadable: %s",
			releaseID, path, apperrors.LogDetail(serr))
	}
	// Row without bytes (deleted/moved data dir): drop the stale row
	// so the next request retries instead of 500ing forever. Only drop
	// the row we served: a concurrent upload, refresh, or delete may
	// have replaced it or recorded a tombstone while we were reading.
	if cur, gerr := h.st.GetCover(ctx, releaseID); gerr == nil && !cur.Removed &&
		cur.MIME == row.MIME && cur.SizeBytes == row.SizeBytes && cur.FetchedAt == row.FetchedAt {
		h.dropStaleCoverRow("cover cache release", releaseID, func() error {
			return h.st.DeleteCover(ctx, releaseID)
		})
	} else if gerr == nil && cur.Removed {
		h.writeAppError(c, apperrors.ErrNotFound)
		return
	} else if apperrors.Is(gerr, apperrors.ErrNotFound) {
		h.writeAppError(c, apperrors.ErrNotFound)
		return
	} else if gerr != nil {
		h.writeAppError(c, fmt.Errorf("cover cache release=%q recheck: %w", releaseID, gerr))
		return
	}
	h.writeAppError(c, apperrors.Wrap(
		apperrors.ErrInternal,
		fmt.Errorf("cover file=%q unreadable", releaseID)),
	)
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

	start := time.Now()
	row, err := h.doRefreshReleaseCover(ctx, releaseID)
	if err != nil {
		h.writeAppError(c, err)
		return
	}

	h.log.Infof("cover refresh release=%q source=%s bytes=%d done in %s; embed queued",
		releaseID, row.Source, row.SizeBytes, time.Since(start).Round(time.Millisecond))
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
	start := time.Now()
	row, err := h.saveReleaseCoverBytes(ctx, releaseID, &artwork.Result{
		Bytes:  img,
		MIME:   mime,
		Source: artwork.SourceUpload,
	})
	if err != nil {
		h.writeAppError(c, fmt.Errorf("cover upload release=%q: %w", releaseID, err))
		return
	}
	h.log.Infof("cover upload release=%q bytes=%d done in %s; embed queued",
		releaseID, row.SizeBytes, time.Since(start).Round(time.Millisecond))
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
	start := time.Now()
	res, err := h.art.FetchURL(ctx, raw)
	if err != nil {
		h.writeAppError(c, fmt.Errorf("cover URL import release=%q: %w", releaseID, err))
		return
	}
	row, err := h.saveReleaseCoverBytes(ctx, releaseID, res)
	if err != nil {
		h.writeAppError(c, fmt.Errorf("cover URL upload release=%q: %w", releaseID, err))
		return
	}
	h.log.Infof("cover URL upload release=%q bytes=%d done in %s; embed queued",
		releaseID, row.SizeBytes, time.Since(start).Round(time.Millisecond))
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
	start := time.Now()
	row, err := h.saveArtistCoverBytes(ctx, artistID, &artwork.Result{
		Bytes:  img,
		MIME:   mime,
		Source: artwork.SourceUpload,
	})
	if err != nil {
		h.writeAppError(c, fmt.Errorf("artist cover upload artist=%q: %w", artistID, err))
		return
	}
	h.log.Infof("artist cover upload artist=%q bytes=%d done in %s",
		artistID, row.SizeBytes, time.Since(start).Round(time.Millisecond))
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
	start := time.Now()
	res, err := h.art.FetchURL(ctx, raw)
	if err != nil {
		h.writeAppError(c, fmt.Errorf("artist cover URL import artist=%q: %w", artistID, err))
		return
	}
	row, err := h.saveArtistCoverBytes(ctx, artistID, res)
	if err != nil {
		h.writeAppError(c, fmt.Errorf("artist cover URL upload artist=%q: %w", artistID, err))
		return
	}
	h.log.Infof("artist cover URL upload artist=%q bytes=%d done in %s",
		artistID, row.SizeBytes, time.Since(start).Round(time.Millisecond))
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
		h.removeCoverFiles(releaseID)
		return nil
	}); err != nil {
		h.writeAppError(c, fmt.Errorf("cover delete release=%q: %w", releaseID, err))
		return
	}
	h.log.Infof("cover delete release=%q done; clear queued", releaseID)
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
		h.removeArtistCoverFiles(artistID)
		return nil
	}); err != nil {
		h.writeAppError(c, fmt.Errorf("artist cover delete artist=%q: %w", artistID, err))
		return
	}
	h.log.Infof("artist cover delete artist=%q done", artistID)
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
// Removal failures only log: the cover GC retries leftovers on the next scan.
func (h *Handler) removeCoverFiles(releaseID string) {
	matches, err := filepath.Glob(filepath.Join(h.coverDir, releaseID+".*"))
	if err != nil {
		h.log.Warnf("cover files release %q glob: %s", releaseID, apperrors.LogDetail(err))
		return
	}
	for _, m := range matches {
		if err := os.Remove(m); err != nil && !os.IsNotExist(err) {
			h.log.Warnf("cover files release %q remove %q: %s",
				releaseID, m, apperrors.LogDetail(err))
		}
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
// carry no-cache so a removed photo never sticks in the browser. Bodies
// stream from disk (Range-capable) instead of loading into memory.
//
// Fast path: a live cached row serves 304/200 without loading the artist
// (a photo row implies its artist exists via FK cascade). The artist is
// only loaded on a cache miss or stale bytes, when the name is needed to
// fetch providers.
func (h *Handler) serveArtistCover(c *gin.Context, artistID string) {
	c.Header("Cache-Control", "no-cache")
	ctx := c.Request.Context()

	if row, err := h.st.GetArtistCover(ctx, artistID); err == nil {
		if row.Removed {
			h.writeAppError(c, apperrors.ErrNotFound)
			return
		}
		etag := coverETag(row.SizeBytes, row.FetchedAt)
		c.Header("ETag", etag)
		if c.GetHeader("If-None-Match") == etag {
			c.Status(http.StatusNotModified)
			return
		}
		if path, perr := config.ArtistCoverPath(h.coverDir, artistID, row.MIME); perr != nil {
			h.log.Warnf("artist cover cache artist %q path: %s", artistID, apperrors.LogDetail(perr))
		} else if served, serr := serveCoverFile(c, h.coverDir, path, row.MIME); served {
			return
		} else {
			h.logCoverFileError("artist cover cache artist", artistID, path, serr)
		}
		// Cached row without readable bytes: drop the stale row so the
		// slow path below refetches. Only drop the row we checked.
		if cur, gerr := h.st.GetArtistCover(ctx, artistID); gerr == nil && !cur.Removed &&
			cur.MIME == row.MIME && cur.SizeBytes == row.SizeBytes && cur.FetchedAt == row.FetchedAt {
			h.dropStaleCoverRow("artist cover cache artist", artistID, func() error {
				return h.st.DeleteArtistCover(ctx, artistID)
			})
		} else if gerr == nil && cur.Removed {
			h.writeAppError(c, apperrors.ErrNotFound)
			return
		} else if gerr != nil && !apperrors.Is(gerr, apperrors.ErrNotFound) {
			h.writeAppError(c, fmt.Errorf("artist cover cache artist=%q recheck: %w", artistID, gerr))
			return
		}
	} else if apperrors.Is(err, apperrors.ErrNotFound) {
		// No cached photo: an active negative-cache entry is a 404
		// without loading the artist or touching providers.
		if m, merr := h.st.GetArtistCoverMiss(ctx, artistID); merr == nil {
			if m.Active(time.Now()) {
				h.writeAppError(c, apperrors.ErrNotFound)
				return
			}
		} else if !apperrors.Is(merr, apperrors.ErrNotFound) {
			h.writeAppError(c, merr)
			return
		}
	} else {
		h.writeAppError(c, err)
		return
	}

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

	etag := coverETag(row.SizeBytes, row.FetchedAt)
	c.Header("ETag", etag)
	if c.GetHeader("If-None-Match") == etag {
		c.Status(http.StatusNotModified)
		return
	}

	path, err := config.ArtistCoverPath(h.coverDir, artistID, row.MIME)
	if err != nil {
		h.writeAppError(c, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("artist cover path artist=%q: %w", artistID, err)),
		)
		return
	}
	if served, serr := serveCoverFile(c, h.coverDir, path, row.MIME); served {
		return
	} else {
		h.log.Warnf("artist cover cache artist %q: just-saved file %q unreadable: %s",
			artistID, path, apperrors.LogDetail(serr))
	}
	// Row without bytes (deleted/moved data dir): drop the stale row
	// so the next request retries instead of 500ing forever. Only drop
	// the row we served: a concurrent upload, refresh, or delete may
	// have replaced it or recorded a tombstone while we were reading.
	if cur, gerr := h.st.GetArtistCover(ctx, artistID); gerr == nil && !cur.Removed &&
		cur.MIME == row.MIME && cur.SizeBytes == row.SizeBytes && cur.FetchedAt == row.FetchedAt {
		h.dropStaleCoverRow("artist cover cache artist", artistID, func() error {
			return h.st.DeleteArtistCover(ctx, artistID)
		})
	} else if gerr == nil && cur.Removed {
		h.writeAppError(c, apperrors.ErrNotFound)
		return
	} else if apperrors.Is(gerr, apperrors.ErrNotFound) {
		h.writeAppError(c, apperrors.ErrNotFound)
		return
	} else if gerr != nil {
		h.writeAppError(c, fmt.Errorf("artist cover cache artist=%q recheck: %w", artistID, gerr))
		return
	}
	h.writeAppError(c, apperrors.Wrap(
		apperrors.ErrInternal,
		fmt.Errorf("artist cover artist=%q unreadable", artistID)),
	)
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

	start := time.Now()
	row, err := tasks.RefreshArtistCover(ctx, h.st, h.art, h.coverDir, artistID, artist.Name)
	if err != nil {
		h.writeAppError(c, err)
		return
	}

	h.log.Infof("artist cover refresh artist=%q source=%s bytes=%d done in %s",
		artistID, row.Source, row.SizeBytes, time.Since(start).Round(time.Millisecond))
	c.JSON(http.StatusOK, row)
}

// removeArtistCoverFiles deletes every cached file for an artist (all
// extensions). Artist files carry the "artist-" prefix, so release files are
// never touched. Removal failures only log: the cover GC retries leftovers.
func (h *Handler) removeArtistCoverFiles(artistID string) {
	matches, err := filepath.Glob(filepath.Join(h.coverDir, "artist-"+artistID+".*"))
	if err != nil {
		h.log.Warnf("artist cover files artist %q glob: %s", artistID, apperrors.LogDetail(err))
		return
	}
	for _, m := range matches {
		if err := os.Remove(m); err != nil && !os.IsNotExist(err) {
			h.log.Warnf("artist cover files artist %q remove %q: %s",
				artistID, m, apperrors.LogDetail(err))
		}
	}
}
