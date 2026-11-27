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
	"github.com/ppablomunoz/coda/internal/tags"
	"github.com/ppablomunoz/coda/internal/utils"
)

// coverLocks serializes cover mutations per release/artist so concurrent
// cache misses (GET handler vs background prefetch), uploads, deletions,
// and refreshes never interleave on the same row/files. Uploads, deletes,
// and refreshes share this lock; Ensure fetches hold it too so a first
// fetch collapses into one provider call and waiters reuse the winner's
// row. Keys are prefixed ("release:"+id, "artist:"+id) so the two ID
// namespaces never collide. Entries are bounded by the number of releases
// and artists ever touched.
var coverLocks sync.Map // "release:<id>" | "artist:<id>" -> *sync.Mutex

func withCoverMutex(key string) *sync.Mutex {
	locked, _ := coverLocks.LoadOrStore(key, &sync.Mutex{})
	return locked.(*sync.Mutex)
}

// WithReleaseCoverLock runs fn while holding the per-release cover lock.
func WithReleaseCoverLock(releaseID string, fn func() error) error {
	mu := withCoverMutex("release:" + releaseID)
	mu.Lock()
	defer mu.Unlock()
	return fn()
}

// WithArtistCoverLock runs fn while holding the per-artist cover lock.
func WithArtistCoverLock(artistID string, fn func() error) error {
	mu := withCoverMutex("artist:" + artistID)
	mu.Lock()
	defer mu.Unlock()
	return fn()
}

// EnsureCover returns the cached cover row for a release, fetching it first
// when missing. A user-removed cover (tombstone) answers ErrNotFound without
// fetching, so a deletion is never undone by a later GET. Concurrent misses
// for one release collapse into a single fetch: the first caller fetches,
// waiters re-check the cache and reuse its row. Artist/album feed the Deezer
// text fallback only. A nil fetcher disables fetching (tests): the existing
// cache still serves, a miss answers ErrNotFound.
func EnsureCover(ctx context.Context, st *store.Store, art *artwork.Fetcher,
	coverDir, releaseID, artist, album string,
) (*store.CoverParams, error) {
	if row, err := st.GetCover(ctx, releaseID); err == nil {
		if row.Removed {
			return nil, apperrors.ErrNotFound
		}
		return row, nil
	} else if !apperrors.Is(err, apperrors.ErrNotFound) {
		return nil, err
	}
	if art == nil {
		return nil, apperrors.ErrNotFound
	}

	var row *store.CoverParams
	err := WithReleaseCoverLock(releaseID, func() error {
		// Another caller may have fetched while we waited.
		if cached, err := st.GetCover(ctx, releaseID); err == nil {
			if cached.Removed {
				return apperrors.ErrNotFound
			}
			row = cached
			return nil
		} else if !apperrors.Is(err, apperrors.ErrNotFound) {
			return err
		}
		res, err := art.Fetch(ctx, releaseID, artist, album)
		if err != nil {
			return err
		}
		if err := SaveCover(ctx, st, coverDir, releaseID, res); err != nil {
			return err
		}
		row, err = st.GetCover(ctx, releaseID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return row, nil
}

// RefreshCover fetches provider art first and only swaps the cache on
// success: the new bytes are written (atomic tmp+rename) and the row is
// upserted, clearing any removal tombstone. A fetch failure or a no-art
// miss returns without touching the existing row or files, so the previous
// upload, refresh, or cached art survives. Callers enqueue the re-embed
// task after a successful return. The whole fetch+save holds the
// per-release lock so concurrent uploads, deletes, and refreshes serialize.
func RefreshCover(ctx context.Context, st *store.Store, art *artwork.Fetcher,
	coverDir, releaseID, artist, album string,
) (*store.CoverParams, error) {
	if art == nil {
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("cover refresh unavailable"),
		)
	}
	var row *store.CoverParams
	err := WithReleaseCoverLock(releaseID, func() error {
		res, err := art.Fetch(ctx, releaseID, artist, album)
		if err != nil {
			return err
		}
		if err := SaveCover(ctx, st, coverDir, releaseID, res); err != nil {
			return err
		}
		r, err := st.GetCover(ctx, releaseID)
		if err != nil {
			return err
		}
		row = r
		return nil
	})
	if err != nil {
		return nil, err
	}
	return row, nil
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
	// Hidden, glob-invisible temp name: removeCoverFiles globs
	// <releaseID>.* and must never see an in-flight temp file.
	tmp, err := os.CreateTemp(coverDir, ".coda-cover-"+releaseID+"-*.tmp")
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
// GET .../cover. Overlapping runs for one artist collapse into one. The
// caller derives ctx from the runner (not the finished import task) with
// its own bound, so the run survives the task but still gives up on
// shutdown; Start waits for it.
func (r *Runner) prefetchCoversBG(ctx context.Context, artistID string) {
	if r.art == nil {
		return
	}
	if !r.claimCoverPrefetch(artistID) {
		return
	}
	defer r.releaseCoverPrefetch(artistID)

	releases, err := r.coverJobs(ctx, artistID)
	if err != nil {
		r.log.Warnf("cover prefetch artist %q list: %v", artistID, err)
		return
	}
	if len(releases) == 0 {
		r.log.Infof("cover prefetch artist %q: nothing to fetch", artistID)
		return
	}

	start := time.Now()
	r.log.Infof("cover prefetch artist %q: %d releases to fetch", artistID, len(releases))
	jobs := make(chan coverJob)
	var wg sync.WaitGroup
	for range coverPrefetchWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				if _, err := EnsureCover(ctx, r.st, r.art, r.coverDir, j.releaseID, j.artist, j.title); err != nil {
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
		case <-ctx.Done():
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

// releaseCoverPrefetch releases the background cover claim for an artist.
func (r *Runner) releaseCoverPrefetch(artistID string) {
	r.coverMu.Lock()
	defer r.coverMu.Unlock()
	delete(r.coverPrefetch, artistID)
}

// embedReleaseCover re-stamps the currently cached release cover into every
// linked file of the release. Best-effort: per-file failures only log, and
// a missing cover or no linked files is a clean no-op. The bytes are loaded
// at run time (not enqueue time) so a later refresh wins over an older
// upload when the two race.
func (r *Runner) embedReleaseCover(ctx context.Context, releaseID string) {
	if err := utils.ValidateMBID(releaseID); err != nil {
		r.log.Warnf("embed cover release %q: invalid id", releaseID)
		return
	}

	row, err := r.st.GetCover(ctx, releaseID)
	if err != nil {
		if !apperrors.Is(err, apperrors.ErrNotFound) {
			r.log.Warnf("embed cover release %q check: %v", releaseID, err)
		}
		return
	}
	if row.Removed {
		// A removal won over the queued upload: leave the tombstone alone.
		return
	}
	path, err := config.CoverPath(r.coverDir, releaseID, row.MIME)
	if err != nil {
		r.log.Warnf("embed cover release %q path: %v", releaseID, err)
		return
	}
	img, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Only drop the row we checked: a concurrent upload, refresh,
			// or delete may have replaced it or recorded a tombstone while
			// we were reading. Never drop a tombstone or a newer row.
			if cur, gerr := r.st.GetCover(ctx, releaseID); gerr == nil &&
				!cur.Removed && cur.MIME == row.MIME &&
				cur.SizeBytes == row.SizeBytes && cur.FetchedAt == row.FetchedAt {
				_ = r.st.DeleteCover(ctx, releaseID)
			}
		} else {
			r.log.Warnf("embed cover release %q read: %v", releaseID, err)
		}
		return
	}

	files, err := r.st.ListFilesByRelease(ctx, releaseID)
	if err != nil {
		r.log.Warnf("embed cover release %q list files: %v", releaseID, err)
		return
	}
	if len(files) == 0 {
		return
	}

	embedded, failed := 0, 0
	for _, f := range files {
		select {
		case <-ctx.Done():
			r.log.Warnf("embed cover release %q: stopped early (%d embedded, %d failed)", releaseID, embedded, failed)
			return
		default:
		}
		if err := tags.WriteCover(f.Path, img); err != nil {
			failed++
			r.log.Warnf("embed cover release %q file=%d: %v", releaseID, f.ID, err)
			continue
		}
		embedded++
	}
	r.log.Infof("embed cover release %q done: %d embedded, %d failed", releaseID, embedded, failed)
}

// clearReleaseCover removes the embedded front cover from every linked file
// of a user-removed release. Best-effort: per-file failures only log, and a
// missing row or no linked files is a clean no-op. The tombstone is checked
// at run time (not enqueue time) so an upload or refresh that won the race
// keeps its art: only a still-removed cover clears files.
func (r *Runner) clearReleaseCover(ctx context.Context, releaseID string) {
	if err := utils.ValidateMBID(releaseID); err != nil {
		r.log.Warnf("clear cover release %q: invalid id", releaseID)
		return
	}

	row, err := r.st.GetCover(ctx, releaseID)
	if err != nil {
		if !apperrors.Is(err, apperrors.ErrNotFound) {
			r.log.Warnf("clear cover release %q check: %v", releaseID, err)
		}
		return
	}
	if !row.Removed {
		return
	}

	files, err := r.st.ListFilesByRelease(ctx, releaseID)
	if err != nil {
		r.log.Warnf("clear cover release %q list files: %v", releaseID, err)
		return
	}
	if len(files) == 0 {
		return
	}

	cleared, failed := 0, 0
	for _, f := range files {
		select {
		case <-ctx.Done():
			r.log.Warnf("clear cover release %q: stopped early (%d cleared, %d failed)", releaseID, cleared, failed)
			return
		default:
		}
		if err := tags.ClearCover(f.Path); err != nil {
			failed++
			r.log.Warnf("clear cover release %q file=%d: %v", releaseID, f.ID, err)
			continue
		}
		cleared++
	}
	r.log.Infof("clear cover release %q done: %d cleared, %d failed", releaseID, cleared, failed)
}
