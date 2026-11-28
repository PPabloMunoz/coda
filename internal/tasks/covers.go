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

// coverMissInitialTTL is the retry delay after the first provider miss; each
// further miss doubles it up to coverMissMaxTTL.
const (
	coverMissInitialTTL = 6 * time.Hour
	coverMissMaxTTL     = 7 * 24 * time.Hour
)

// coverMissBackoff returns the retry delay for the given 1-based strike count.
func coverMissBackoff(misses int) time.Duration {
	if misses < 1 {
		misses = 1
	}
	d := coverMissInitialTTL
	for i := 1; i < misses; i++ {
		d *= 2
		if d >= coverMissMaxTTL {
			return coverMissMaxTTL
		}
	}
	if d > coverMissMaxTTL {
		return coverMissMaxTTL
	}
	return d
}

// isReleaseMissActive reports whether a recorded provider miss still
// suppresses fetches for a release. Expired misses stay stored (their strike
// count drives the next backoff) but no longer suppress.
func isReleaseMissActive(ctx context.Context, st *store.Store, releaseID string) (bool, error) {
	m, err := st.GetReleaseCoverMiss(ctx, releaseID)
	if err != nil {
		if apperrors.Is(err, apperrors.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return m.Active(time.Now()), nil
}

// recordReleaseCoverMiss increments the miss strike count for a release and
// pushes its retry time out by the backoff. Best-effort: a persistence
// failure only logs, so a miss that cannot be recorded simply refetches.
func recordReleaseCoverMiss(ctx context.Context, st *store.Store, art *artwork.Fetcher, releaseID string) {
	misses := 0
	if m, err := st.GetReleaseCoverMiss(ctx, releaseID); err == nil {
		misses = m.Misses
	} else if !apperrors.Is(err, apperrors.ErrNotFound) {
		return
	}
	misses++
	now := time.Now()
	if err := st.UpsertReleaseCoverMiss(ctx, releaseID, misses, now, now.Add(coverMissBackoff(misses))); err != nil {
		return
	}
	if art != nil {
		art.Debugf("artwork: no art for release %s, miss %d cached", releaseID, misses)
	}
}

// isArtistMissActive reports whether a recorded provider miss still
// suppresses photo fetches for an artist.
func isArtistMissActive(ctx context.Context, st *store.Store, artistID string) (bool, error) {
	m, err := st.GetArtistCoverMiss(ctx, artistID)
	if err != nil {
		if apperrors.Is(err, apperrors.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return m.Active(time.Now()), nil
}

// recordArtistCoverMiss increments the miss strike count for an artist.
// Best-effort: persistence failures only log.
func recordArtistCoverMiss(ctx context.Context, st *store.Store, art *artwork.Fetcher, artistID string) {
	misses := 0
	if m, err := st.GetArtistCoverMiss(ctx, artistID); err == nil {
		misses = m.Misses
	} else if !apperrors.Is(err, apperrors.ErrNotFound) {
		return
	}
	misses++
	now := time.Now()
	if err := st.UpsertArtistCoverMiss(ctx, artistID, misses, now, now.Add(coverMissBackoff(misses))); err != nil {
		return
	}
	if art != nil {
		art.Debugf("artwork: no photo for artist %s, miss %d cached", artistID, misses)
	}
}

// EnsureCover returns the cached cover row for a release, fetching it first
// when missing. The fetched flag reports whether this call fetched and
// saved fresh bytes (vs serving the cache): callers queue the background
// re-embed only on a fresh fetch, so linked files pick up art that arrived
// after they were linked. A user-removed cover (tombstone) answers
// ErrNotFound without fetching, so a deletion is never undone by a later
// GET; a recorded provider miss does the same until its retry time.
// Concurrent misses for one release collapse into a single fetch: the first
// caller fetches, waiters re-check the cache and reuse its row.
// Artist/album feed the Deezer text fallback only. A nil fetcher disables
// fetching (tests): the existing cache still serves, a miss answers
// ErrNotFound.
func EnsureCover(ctx context.Context, st *store.Store, art *artwork.Fetcher,
	coverDir, releaseID, artist, album string,
) (row *store.CoverParams, fetched bool, err error) {
	if row, err := st.GetCover(ctx, releaseID); err == nil {
		if row.Removed {
			return nil, false, apperrors.ErrNotFound
		}
		if art != nil {
			art.Debugf("artwork: cover cache hit release %s", releaseID)
		}
		return row, false, nil
	} else if !apperrors.Is(err, apperrors.ErrNotFound) {
		return nil, false, err
	}
	if active, err := isReleaseMissActive(ctx, st, releaseID); err != nil {
		return nil, false, err
	} else if active {
		if art != nil {
			art.Debugf("artwork: cover miss cache hit release %s", releaseID)
		}
		return nil, false, apperrors.ErrNotFound
	}
	if art == nil {
		return nil, false, apperrors.ErrNotFound
	}

	var didFetch bool
	err = WithReleaseCoverLock(releaseID, func() error {
		// Another caller may have fetched while we waited.
		if cached, err := st.GetCover(ctx, releaseID); err == nil {
			if cached.Removed {
				return apperrors.ErrNotFound
			}
			if art != nil {
				art.Debugf("artwork: cover cache hit release %s", releaseID)
			}
			row = cached
			return nil
		} else if !apperrors.Is(err, apperrors.ErrNotFound) {
			return err
		}
		if active, err := isReleaseMissActive(ctx, st, releaseID); err != nil {
			return err
		} else if active {
			art.Debugf("artwork: cover miss cache hit release %s", releaseID)
			return apperrors.ErrNotFound
		}
		res, err := art.Fetch(ctx, releaseID, artist, album)
		if err != nil {
			if apperrors.Is(err, apperrors.ErrNotFound) {
				recordReleaseCoverMiss(ctx, st, art, releaseID)
			}
			return fmt.Errorf("cover fetch release=%q artist=%q album=%q: %w",
				releaseID, artist, album, err)
		}
		if err := SaveCover(ctx, st, coverDir, releaseID, res); err != nil {
			return fmt.Errorf("cover save release=%q: %w", releaseID, err)
		}
		row, err = st.GetCover(ctx, releaseID)
		if err != nil {
			return fmt.Errorf("cover reload release=%q: %w", releaseID, err)
		}
		didFetch = true
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return row, didFetch, nil
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
			if apperrors.Is(err, apperrors.ErrNotFound) {
				// Record the miss only when nothing is cached: an
				// existing upload or tombstone already determines the
				// next GET, so it must survive the refresh.
				if _, gerr := st.GetCover(ctx, releaseID); apperrors.Is(gerr, apperrors.ErrNotFound) {
					recordReleaseCoverMiss(ctx, st, art, releaseID)
				}
			}
			return fmt.Errorf("cover refresh release=%q artist=%q album=%q (previous cover kept): %w",
				releaseID, artist, album, err)
		}
		if err := SaveCover(ctx, st, coverDir, releaseID, res); err != nil {
			return fmt.Errorf("cover refresh release=%q artist=%q album=%q save: %w",
				releaseID, artist, album, err)
		}
		r, err := st.GetCover(ctx, releaseID)
		if err != nil {
			return fmt.Errorf("cover refresh release=%q reload: %w", releaseID, err)
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
// DB row. Bytes are normalized first (max 1200px JPEG), so raw uploads —
// the one path that bypasses the fetcher — land in the same compact form
// as provider art; fetcher results pass through unchanged. A DB failure
// removes the just-written file so rows and bytes never drift apart.
func SaveCover(ctx context.Context, st *store.Store, coverDir, releaseID string,
	res *artwork.Result,
) error {
	if res == nil || len(res.Bytes) == 0 {
		return apperrors.Wrap(
			apperrors.ErrInvalidInput,
			fmt.Errorf("artwork: empty result"),
		)
	}
	norm, mime, err := artwork.Normalize(res.Bytes)
	if err != nil {
		return apperrors.Wrap(
			apperrors.ErrInvalidInput,
			fmt.Errorf("artwork: unusable cover image: %w", err),
		)
	}
	res = &artwork.Result{Bytes: norm, MIME: mime, Source: res.Source, SourceURL: res.SourceURL}
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
		r.log.Warnf("cover prefetch artist %q list: %s", artistID, apperrors.LogDetail(err))
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
				if _, _, err := EnsureCover(ctx, r.st, r.art, r.coverDir, j.releaseID, j.artist, j.title); err != nil {
					if !apperrors.Is(err, apperrors.ErrNotFound) {
						r.log.Warnf("cover prefetch release %q: %s", j.releaseID, apperrors.LogDetail(err))
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
				r.log.Warnf("cover prefetch release %q check: %s", g.Release.ID, apperrors.LogDetail(err))
				continue
			}
			if active, err := isReleaseMissActive(ctx, r.st, g.Release.ID); err != nil {
				r.log.Warnf("cover prefetch release %q miss check: %s", g.Release.ID, apperrors.LogDetail(err))
				continue
			} else if active {
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
			r.log.Warnf("embed cover release %q check: %s", releaseID, apperrors.LogDetail(err))
		}
		return
	}
	if row.Removed {
		// A removal won over the queued upload: leave the tombstone alone.
		return
	}
	path, err := config.CoverPath(r.coverDir, releaseID, row.MIME)
	if err != nil {
		r.log.Warnf("embed cover release %q path: %s", releaseID, apperrors.LogDetail(err))
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
			r.log.Warnf("embed cover release %q read: %s", releaseID, apperrors.LogDetail(err))
		}
		return
	}

	files, err := r.st.ListFilesByRelease(ctx, releaseID)
	if err != nil {
		r.log.Warnf("embed cover release %q list files: %s", releaseID, apperrors.LogDetail(err))
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
			r.log.Warnf("embed cover release %q file=%d: %s", releaseID, f.ID, apperrors.LogDetail(err))
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
			r.log.Warnf("clear cover release %q check: %s", releaseID, apperrors.LogDetail(err))
		}
		return
	}
	if !row.Removed {
		return
	}

	files, err := r.st.ListFilesByRelease(ctx, releaseID)
	if err != nil {
		r.log.Warnf("clear cover release %q list files: %s", releaseID, apperrors.LogDetail(err))
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
			r.log.Warnf("clear cover release %q file=%d: %s", releaseID, f.ID, apperrors.LogDetail(err))
			continue
		}
		cleared++
	}
	r.log.Infof("clear cover release %q done: %d cleared, %d failed", releaseID, cleared, failed)
}
