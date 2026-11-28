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

// EnsureArtistCover returns the cached photo row for an artist, fetching it
// first when missing. A user-removed photo (tombstone) answers ErrNotFound
// without fetching, so a deletion is never undone by a later GET; a recorded
// provider miss does the same until its retry time. A nil fetcher disables
// fetching: the existing cache still serves, a miss answers ErrNotFound.
// Display-only: artist photos are never embedded into audio files.
func EnsureArtistCover(ctx context.Context, st *store.Store, art *artwork.Fetcher,
	coverDir, artistID, artistName string,
) (*store.ArtistCoverParams, error) {
	if row, err := st.GetArtistCover(ctx, artistID); err == nil {
		if row.Removed {
			return nil, apperrors.ErrNotFound
		}
		if art != nil {
			art.Debugf("artwork: artist cover cache hit %s", artistID)
		}
		return row, nil
	} else if !apperrors.Is(err, apperrors.ErrNotFound) {
		return nil, err
	}
	if active, err := isArtistMissActive(ctx, st, artistID); err != nil {
		return nil, err
	} else if active {
		if art != nil {
			art.Debugf("artwork: artist miss cache hit %s", artistID)
		}
		return nil, apperrors.ErrNotFound
	}
	if art == nil {
		return nil, apperrors.ErrNotFound
	}

	var row *store.ArtistCoverParams
	err := WithArtistCoverLock(artistID, func() error {
		// Another caller may have fetched while we waited.
		if cached, err := st.GetArtistCover(ctx, artistID); err == nil {
			if cached.Removed {
				return apperrors.ErrNotFound
			}
			art.Debugf("artwork: artist cover cache hit %s", artistID)
			row = cached
			return nil
		} else if !apperrors.Is(err, apperrors.ErrNotFound) {
			return err
		}
		if active, err := isArtistMissActive(ctx, st, artistID); err != nil {
			return err
		} else if active {
			art.Debugf("artwork: artist miss cache hit %s", artistID)
			return apperrors.ErrNotFound
		}
		res, err := art.FetchArtist(ctx, artistID, artistName)
		if err != nil {
			if apperrors.Is(err, apperrors.ErrNotFound) {
				recordArtistCoverMiss(ctx, st, art, artistID)
			}
			return fmt.Errorf("artist cover fetch artist=%q name=%q: %w",
				artistID, artistName, err)
		}
		if err := SaveArtistCover(ctx, st, coverDir, artistID, res); err != nil {
			return fmt.Errorf("artist cover save artist=%q: %w", artistID, err)
		}
		r, err := st.GetArtistCover(ctx, artistID)
		if err != nil {
			return fmt.Errorf("artist cover reload artist=%q: %w", artistID, err)
		}
		row = r
		return nil
	})
	if err != nil {
		return nil, err
	}
	return row, nil
}

// RefreshArtistCover fetches a provider photo first and only swaps the
// cache on success, mirroring RefreshCover. A fetch failure or a no-photo
// miss returns without touching the existing row or files, so the previous
// upload or cached photo survives. The whole fetch+save holds the
// per-artist lock so concurrent uploads, deletes, and refreshes serialize.
func RefreshArtistCover(ctx context.Context, st *store.Store, art *artwork.Fetcher,
	coverDir, artistID, artistName string,
) (*store.ArtistCoverParams, error) {
	if art == nil {
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("artist cover refresh unavailable"),
		)
	}
	var row *store.ArtistCoverParams
	err := WithArtistCoverLock(artistID, func() error {
		res, err := art.FetchArtist(ctx, artistID, artistName)
		if err != nil {
			if apperrors.Is(err, apperrors.ErrNotFound) {
				// Record the miss only when nothing is cached: an
				// existing upload or tombstone already determines the
				// next GET, so it must survive the refresh.
				if _, gerr := st.GetArtistCover(ctx, artistID); apperrors.Is(gerr, apperrors.ErrNotFound) {
					recordArtistCoverMiss(ctx, st, art, artistID)
				}
			}
			return fmt.Errorf("artist cover refresh artist=%q name=%q (previous photo kept): %w",
				artistID, artistName, err)
		}
		if err := SaveArtistCover(ctx, st, coverDir, artistID, res); err != nil {
			return fmt.Errorf("artist cover refresh artist=%q name=%q save: %w",
				artistID, artistName, err)
		}
		r, err := st.GetArtistCover(ctx, artistID)
		if err != nil {
			return fmt.Errorf("artist cover refresh artist=%q reload: %w", artistID, err)
		}
		row = r
		return nil
	})
	if err != nil {
		return nil, err
	}
	return row, nil
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
	// Hidden, glob-invisible temp name: removeArtistCoverFiles globs
	// artist-<id>.* and must never see an in-flight temp file.
	tmp, err := os.CreateTemp(coverDir, ".coda-artist-cover-"+artistID+"-*.tmp")
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
// The caller derives ctx from the runner with its own bound — the run
// survives the finished import task but still gives up on shutdown.
func (r *Runner) prefetchArtistCover(ctx context.Context, artistID, artistName string) {
	if r.art == nil {
		return
	}

	if _, err := r.st.GetArtistCover(ctx, artistID); err == nil {
		return
	} else if !apperrors.Is(err, apperrors.ErrNotFound) {
		r.log.Warnf("artist cover prefetch artist %q check: %s", artistID, apperrors.LogDetail(err))
		return
	}
	if active, err := isArtistMissActive(ctx, r.st, artistID); err != nil {
		r.log.Warnf("artist cover prefetch artist %q miss check: %s", artistID, apperrors.LogDetail(err))
		return
	} else if active {
		return
	}
	if _, err := EnsureArtistCover(ctx, r.st, r.art, r.coverDir, artistID, artistName); err != nil {
		if !apperrors.Is(err, apperrors.ErrNotFound) {
			r.log.Warnf("artist cover prefetch artist %q: %s", artistID, apperrors.LogDetail(err))
		}
	}
}
