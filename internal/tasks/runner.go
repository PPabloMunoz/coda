package tasks

import (
	"context"
	"sync"
	"time"

	"github.com/ppablomunoz/coda/internal/artwork"
	apperrors "github.com/ppablomunoz/coda/internal/errors"
	"github.com/ppablomunoz/coda/internal/library"
	"github.com/ppablomunoz/coda/internal/musicbrainz"
	"github.com/ppablomunoz/coda/internal/store"
	"github.com/ppablomunoz/coda/internal/utils"
)

type Runner struct {
	queue    *Queue
	st       *store.Store
	mb       *musicbrainz.Client // same instance as server, don't make two (rate limiter is per-client)
	art      *artwork.Fetcher    // nil disables cover prefetch
	coverDir string
	// coverPrefetch tracks background cover runs; the import task never
	// waits for art, so overlapping refreshes must not stack duplicate runs.
	coverMu       sync.Mutex
	coverPrefetch map[string]struct{}
	log           *utils.Logger
}

// NewRunner returns a Runner dispatching queued scan/import/prune work.
func NewRunner(st *store.Store, mb *musicbrainz.Client, l *utils.Logger, q *Queue,
	art *artwork.Fetcher, coverDir string,
) *Runner {
	return &Runner{
		queue:    q,
		st:       st,
		mb:       mb,
		art:      art,
		coverDir: coverDir,
		log:      l,
	}
}

// Start runs queued tasks serially until ctx is canceled.
func (r *Runner) Start(ctx context.Context) {
	r.queue.run(ctx, r.exec)
}

// exec dispatches a task with per-kind timeouts (10m scan, 30m import).
func (r *Runner) exec(ctx context.Context, t Task) {
	switch t.Key {
	case TaskScanRoots:
		sctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		defer cancel()
		r.scanRoots(sctx, t.ForcePrune)
	case TaskImportDiscography:
		sctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
		defer cancel()
		r.importArtist(sctx, t)
	case TaskPruneArtists:
		r.pruneArtists(ctx)
	case TaskDeleteInactiveArtists:
		r.deleteInactiveArtists(ctx)
	case TaskEmbedReleaseCover:
		sctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		defer cancel()
		r.embedReleaseCover(sctx, t.ID)
	case TaskClearReleaseCover:
		sctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		defer cancel()
		r.clearReleaseCover(sctx, t.ID)
	case TaskRelinkRelease:
		sctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		defer cancel()
		r.relinkRelease(sctx, t.ID)
	default:
		r.log.Warnf("task unknown key %q, skipping", t.Key)
	}
}

// scanRoots walks configured roots, syncs DB rows by path, auto-links
// exact-MBID files (tag rewrite + move), prunes missing files, and sweeps
// orphan credits/collaborators. Roots whose walk is incomplete never prune;
// roots whose complete walk yields zero files keep their rows unless
// forcePrune is set.
func (r *Runner) scanRoots(ctx context.Context, forcePrune bool) {
	r.log.Info("scanning root folders...")

	start := time.Now()
	scanStart := time.Now().UTC().Format("2006-01-02 15:04:05")

	roots := r.st.RootPaths()
	files := []store.ScannedFile{}
	incomplete := []string{}
	if len(roots) > 0 {
		r.cleanupStaleTemps(ctx)
		statuses, err := library.Scan(ctx, roots, func(f library.File) error {
			files = append(files, store.ScannedFile{
				LibraryRoot: f.LibraryRoot,
				Path:        f.Path,
				SizeBytes:   f.SizeBytes,
				MtimeNs:     f.MtimeNs,
				FormatHint:  f.FormatHint,
			})
			return nil
		})
		if err != nil {
			r.log.Errorf("scan failed in %s: %v", time.Since(start), err)
			return
		}
		for _, s := range statuses {
			if !s.Walked {
				r.log.Warnf("scan root=%q walk incomplete, skipping prune for its rows: %v", s.Root, s.WalkErr)
				incomplete = append(incomplete, s.Root)
			}
		}
	}

	stats, err := r.st.SyncLocalFiles(ctx, files, scanStart, store.SyncOptions{
		IncompleteRoots: incomplete,
		ForcePrune:      forcePrune,
	})
	if err != nil {
		r.log.Errorf("scan failed in %s: %v", time.Since(start), err)
		return
	}
	auto := r.autoLinkMoves(ctx)
	r.cleanupCredits(ctx)
	r.pruneArtists(ctx)
	r.log.Infof("scan done: found=%d upserted=%d pruned=%d failed=%d kept_roots=%d autolinked=%d moved=%d skipped=%d in %s",
		stats.Found, stats.Upserted, stats.Pruned, len(stats.Failed), len(stats.KeptRoots), auto.Linked, auto.Moved, auto.SkippedConflict, time.Since(start))
}

// importArtist imports an artist's discography, then prefetches covers in the
// background; removed/non-library artists are skipped as done.
func (r *Runner) importArtist(ctx context.Context, t Task) {
	if t.ID == "" {
		r.log.Warn("task import-discography missing artist id, skipping")
		return
	}

	start := time.Now()
	r.log.Infof("importing artist discography %q", t.ID)

	r.queue.markArtistImportRunning(t.ID)
	art, err := r.st.GetArtistByID(ctx, t.ID)
	if err != nil {
		if apperrors.Is(err, apperrors.ErrNotFound) {
			r.log.Infof("skipping discography import for inactive artist %q", t.ID)
			r.queue.ForgetArtistImport(t.ID)
			return
		}
		r.queue.markArtistImportFailed(t.ID, err)
		r.log.Errorf("importing artist discography %q failed in %s: %v", t.ID, time.Since(start), err)
		return
	}
	if art.InLibrary != 1 {
		r.log.Infof("skipping discography import for non-library artist %q", t.ID)
		r.queue.markArtistImportDone(t.ID)
		return
	}
	if err := r.importDiscography(ctx, t.ID); err != nil {
		// The artist was removed mid-fetch (or is permanently deleted):
		// the save wrote nothing, so forget the import instead of
		// leaving it failed and resurrecting anything.
		if apperrors.Is(err, apperrors.ErrNotFound) {
			r.log.Infof("skipping discography import for removed artist %q", t.ID)
			r.queue.ForgetArtistImport(t.ID)
			return
		}
		r.queue.markArtistImportFailed(t.ID, err)
		r.log.Errorf("importing artist discography %q failed in %s: %v", t.ID, time.Since(start), err)
		return
	}
	// Covers fill in in the background: the import is done as far as status
	// is concerned, and the serial task queue stays free for other work.
	// Gaps are always backfilled lazily on first GET .../cover.
	go r.prefetchCoversBG(ctx, t.ID)
	go r.prefetchArtistCover(ctx, t.ID, art.Name)
	r.cleanupCredits(ctx)
	r.pruneArtists(ctx)
	r.queue.markArtistImportDone(t.ID)
	r.log.Infof("discography for %q saved in %s", t.ID, time.Since(start))
}

// pruneArtists soft-deletes collaboration-only artists with no track left.
func (r *Runner) pruneArtists(ctx context.Context) {
	n, err := r.st.PruneOrphanCollaborators(ctx)
	if err != nil {
		r.log.Errorf("pruning orphan collaborators: %v", err)
		return
	}
	if n > 0 {
		r.log.Infof("pruned %d orphan collaborators", n)
	}
}

// cleanupCredits deletes artist_credits rows no longer referenced by any entity.
func (r *Runner) cleanupCredits(ctx context.Context) {
	n, err := r.st.CleanupOrphanCredits(ctx)
	if err != nil {
		r.log.Errorf("cleaning orphan artist credits: %v", err)
		return
	}
	if n > 0 {
		r.log.Infof("cleaned %d orphan artist credits", n)
	}
}

// deleteInactiveArtists hard-deletes soft-deleted artists with no credits left.
func (r *Runner) deleteInactiveArtists(ctx context.Context) {
	n, err := r.st.DeleteInactiveArtists(ctx)
	if err != nil {
		r.log.Errorf("deleting inactive artists: %v", err)
		return
	}
	if n > 0 {
		r.log.Infof("deleted %d artists", n)
	}
}
