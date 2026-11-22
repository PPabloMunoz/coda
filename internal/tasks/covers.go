package tasks

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ppablomunoz/coda/internal/artwork"
	"github.com/ppablomunoz/coda/internal/config"
	apperrors "github.com/ppablomunoz/coda/internal/errors"
	"github.com/ppablomunoz/coda/internal/store"
	"github.com/ppablomunoz/coda/internal/utils"
)

// EnsureCover returns the cached cover row for a release, fetching it first
// when missing. Artist/album feed the Deezer text fallback only. A nil
// fetcher disables fetching (tests): the existing cache still serves, a
// miss answers ErrNotFound.
func EnsureCover(ctx context.Context, st *store.Store, art *artwork.Fetcher,
	coverDir, releaseID, artist, album string,
) (*store.CoverParams, error) {
	if row, err := st.GetCover(ctx, releaseID); err == nil {
		return row, nil
	} else if !apperrors.Is(err, apperrors.ErrNotFound) {
		return nil, err
	}
	if art == nil {
		return nil, apperrors.ErrNotFound
	}
	res, err := art.Fetch(ctx, releaseID, artist, album)
	if err != nil {
		return nil, err
	}
	if err := SaveCover(ctx, st, coverDir, releaseID, res); err != nil {
		return nil, err
	}
	return st.GetCover(ctx, releaseID)
}

// SaveCover writes fetched bytes to the cache dir (atomic tmp+rename,
// dropping stale sibling extensions from an earlier MIME) and upserts the
// DB row. A DB failure removes the just-written file so rows and bytes
// never drift apart.
func SaveCover(ctx context.Context, st *store.Store, coverDir, releaseID string,
	res *artwork.Result,
) error {
	if res == nil || len(res.Bytes) == 0 {
		return apperrors.Wrap(
			apperrors.ErrInvalidInput,
			fmt.Errorf("artwork: empty result"),
		)
	}
	path, err := config.CoverPath(coverDir, releaseID, res.MIME)
	if err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("cover path: %w", err),
		)
	}
	if err := os.MkdirAll(coverDir, 0o755); err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("cover mkdir: %w", err),
		)
	}
	tmp, err := os.CreateTemp(coverDir, releaseID+"-*.tmp")
	if err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("cover temp file: %w", err),
		)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(res.Bytes); err != nil {
		_ = tmp.Close()
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("cover write: %w", err),
		)
	}
	if err := tmp.Close(); err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("cover write: %w", err),
		)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("cover save: %w", err),
		)
	}
	// Drop stale siblings (same release, earlier MIME/extension).
	if matches, _ := filepath.Glob(filepath.Join(coverDir, releaseID+".*")); matches != nil {
		for _, m := range matches {
			if m != path && !strings.HasSuffix(m, ".tmp") {
				_ = os.Remove(m)
			}
		}
	}
	if err := st.UpsertCover(ctx, store.CoverParams{
		ReleaseID: releaseID,
		Source:    res.Source,
		SourceURL: utils.NullIfEmpty(res.SourceURL),
		MIME:      res.MIME,
		SizeBytes: int64(len(res.Bytes)),
	}); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}

// prefetchCovers caches one cover per imported release. Best-effort by
// design: failures only log, so art never blocks or fails an import.
// Releases that already have a cached cover are skipped.
func (r *Runner) prefetchCovers(ctx context.Context, releases []store.FullReleaseParams) {
	if r.art == nil {
		return
	}
	for i := range releases {
		rel := &releases[i]
		if ctx.Err() != nil {
			return
		}
		if _, err := r.st.GetCover(ctx, rel.ID); err == nil {
			continue
		} else if !apperrors.Is(err, apperrors.ErrNotFound) {
			r.log.Warnf("cover prefetch release %q check: %v", rel.ID, err)
			continue
		}
		artist := creditDisplayName(rel.Credit)
		if artist == "" {
			artist = creditDisplayName(rel.Group.Credit)
		}
		if _, err := EnsureCover(ctx, r.st, r.art, r.coverDir, rel.ID, artist, rel.Title); err != nil {
			if !apperrors.Is(err, apperrors.ErrNotFound) {
				r.log.Warnf("cover prefetch release %q: %v", rel.ID, err)
			}
		}
	}
}

// creditDisplayName renders the joined credit display names ("A & B") for
// text-search fallbacks. Empty when the release has no usable credit.
func creditDisplayName(entries []store.CreditEntry) string {
	display, _, _ := creditDisplay(entries)
	return strings.TrimSpace(display)
}
