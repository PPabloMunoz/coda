package tasks

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

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

// coverPrefetchWorkers bounds parallel cover downloads. Network dominates,
// so a small pool cuts a big-discography prefetch to a fraction of the
// sequential time without stressing provider rate budgets (iTunes ~20/min
// only fires on double-misses).
const coverPrefetchWorkers = 4

type coverJob struct {
	releaseID string
	artist    string
	title     string
}

// prefetchCoversBG caches one cover per release of an artist in the
// background: it never blocks the import task (which already reported done)
// nor the serial task queue. Releases are reloaded from the store rather
// than passed in, so the run is self-contained. Best-effort by design:
// failures only log, and gaps are always backfilled lazily on first
// GET .../cover. Overlapping runs for one artist collapse into one.
func (r *Runner) prefetchCoversBG(ctx context.Context, artistID string) {
	if r.art == nil {
		return
	}
	if !r.claimCoverPrefetch(artistID) {
		return
	}
	defer r.releaseCoverPrefetch(artistID)

	// Detached: the import task is over; this run must survive its
	// cancellation but still give up on shutdown within a bound.
	bctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Minute)
	defer cancel()

	releases, err := r.coverJobs(bctx, artistID)
	if err != nil {
		r.log.Warnf("cover prefetch artist %q list: %v", artistID, err)
		return
	}
	if len(releases) == 0 {
		return
	}

	start := time.Now()
	jobs := make(chan coverJob)
	var wg sync.WaitGroup
	for range coverPrefetchWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				if _, err := EnsureCover(bctx, r.st, r.art, r.coverDir, j.releaseID, j.artist, j.title); err != nil {
					if !apperrors.Is(err, apperrors.ErrNotFound) {
						r.log.Warnf("cover prefetch release %q: %v", j.releaseID, err)
					}
				}
			}
		}()
	}
feed:
	for _, j := range releases {
		select {
		case jobs <- j:
		case <-bctx.Done():
			break feed
		}
	}
	close(jobs)
	wg.Wait()
	r.log.Infof("cover prefetch artist %q done: %d releases in %s", artistID, len(releases), time.Since(start))
}

// coverJobs lists the releases of an artist that still need a cover, derived
// from saved release groups (one release per group). Group credit stands in
// for the release credit: close enough for text-search fallbacks.
func (r *Runner) coverJobs(ctx context.Context, artistID string) ([]coverJob, error) {
	jobs := []coverJob{}
	for offset := 0; ; {
		groups, total, err := r.st.ListReleaseGroups(ctx, store.ReleaseGroupListOptions{
			ArtistID:       artistID,
			IncludeRelease: true,
			PageOptions:    store.PageOptions{Limit: 100, Offset: offset},
		})
		if err != nil {
			return nil, err
		}
		for _, g := range groups {
			if g.Release == nil {
				continue
			}
			if _, err := r.st.GetCover(ctx, g.Release.ID); err == nil {
				continue
			} else if !apperrors.Is(err, apperrors.ErrNotFound) {
				r.log.Warnf("cover prefetch release %q check: %v", g.Release.ID, err)
				continue
			}
			jobs = append(jobs, coverJob{
				releaseID: g.Release.ID,
				artist:    CreditDisplayName(g.Credit),
				title:     g.Release.Title,
			})
		}
		offset += len(groups)
		if offset >= total || len(groups) == 0 {
			break
		}
	}
	return jobs, nil
}

// claimCoverPrefetch registers a background cover run; a second concurrent
// run for the same artist is declined (the first covers it).
func (r *Runner) claimCoverPrefetch(artistID string) bool {
	r.coverMu.Lock()
	defer r.coverMu.Unlock()
	if r.coverPrefetch == nil {
		r.coverPrefetch = make(map[string]struct{})
	}
	if _, ok := r.coverPrefetch[artistID]; ok {
		return false
	}
	r.coverPrefetch[artistID] = struct{}{}
	return true
}

func (r *Runner) releaseCoverPrefetch(artistID string) {
	r.coverMu.Lock()
	defer r.coverMu.Unlock()
	delete(r.coverPrefetch, artistID)
}
