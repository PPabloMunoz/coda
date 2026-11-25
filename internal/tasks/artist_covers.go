package tasks

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ppablomunoz/coda/internal/artwork"
	"github.com/ppablomunoz/coda/internal/config"
	apperrors "github.com/ppablomunoz/coda/internal/errors"
	"github.com/ppablomunoz/coda/internal/store"
	"github.com/ppablomunoz/coda/internal/utils"
)

// EnsureArtistCover returns the cached photo row for an artist, fetching it
// first when missing. A nil fetcher disables fetching: the existing cache
// still serves, a miss answers ErrNotFound. Display-only: artist photos are
// never embedded into audio files.
func EnsureArtistCover(ctx context.Context, st *store.Store, art *artwork.Fetcher,
	coverDir, artistID, artistName string,
) (*store.ArtistCoverParams, error) {
	if row, err := st.GetArtistCover(ctx, artistID); err == nil {
		return row, nil
	} else if !apperrors.Is(err, apperrors.ErrNotFound) {
		return nil, err
	}
	if art == nil {
		return nil, apperrors.ErrNotFound
	}
	res, err := art.FetchArtist(ctx, artistID, artistName)
	if err != nil {
		return nil, err
	}
	if err := SaveArtistCover(ctx, st, coverDir, artistID, res); err != nil {
		return nil, err
	}
	return st.GetArtistCover(ctx, artistID)
}

// SaveArtistCover writes fetched bytes to the cache dir (atomic tmp+rename,
// dropping stale sibling extensions) and upserts the DB row.
func SaveArtistCover(ctx context.Context, st *store.Store, coverDir, artistID string,
	res *artwork.Result,
) error {
	if res == nil || len(res.Bytes) == 0 {
		return apperrors.Wrap(
			apperrors.ErrInvalidInput,
			fmt.Errorf("artwork: empty result"),
		)
	}
	path, err := config.ArtistCoverPath(coverDir, artistID, res.MIME)
	if err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("artist cover path: %w", err),
		)
	}
	if err := os.MkdirAll(coverDir, 0o755); err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("artist cover mkdir: %w", err),
		)
	}
	tmp, err := os.CreateTemp(coverDir, "artist-"+artistID+"-*.tmp")
	if err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("artist cover temp file: %w", err),
		)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(res.Bytes); err != nil {
		_ = tmp.Close()
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("artist cover write: %w", err),
		)
	}
	if err := tmp.Close(); err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("artist cover write: %w", err),
		)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("artist cover save: %w", err),
		)
	}
	if matches, _ := filepath.Glob(filepath.Join(coverDir, "artist-"+artistID+".*")); matches != nil {
		for _, m := range matches {
			if m != path && !strings.HasSuffix(m, ".tmp") {
				_ = os.Remove(m)
			}
		}
	}
	if err := st.UpsertArtistCover(ctx, store.ArtistCoverParams{
		ArtistID:  artistID,
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

// prefetchArtistCover caches one photo for the imported artist. Best-effort:
// failures only log, so art never blocks or fails an import.
// prefetchArtistCover fetches the artist photo when missing. Best-effort:
// failures only log, and gaps backfill lazily on first GET .../cover.
// Detached from the import task by the caller — the run survives the
// task's cancellation but gives up within a bound.
func (r *Runner) prefetchArtistCover(ctx context.Context, artistID, artistName string) {
	if r.art == nil {
		return
	}
	bctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
	defer cancel()

	if _, err := r.st.GetArtistCover(bctx, artistID); err == nil {
		return
	} else if !apperrors.Is(err, apperrors.ErrNotFound) {
		r.log.Warnf("artist cover prefetch artist %q check: %v", artistID, err)
		return
	}
	if _, err := EnsureArtistCover(bctx, r.st, r.art, r.coverDir, artistID, artistName); err != nil {
		if !apperrors.Is(err, apperrors.ErrNotFound) {
			r.log.Warnf("artist cover prefetch artist %q: %v", artistID, err)
		}
	}
}
