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
	// scanTimeout bounds the whole scan (walk + sync + auto-link + prune).
	scanTimeout time.Duration
	// baseCtx is the queue-run context from Start. Detached cover work
	// derives from it (not from the finished import task) so it survives
	// the task but still cancels on shutdown.
	baseCtx context.Context
	// bgWg tracks detached cover runs so Start returns only after they
	// stop and the store is never closed while they still use it.
	bgWg sync.WaitGroup
	// coverPrefetch tracks background cover runs; the import task never
	// waits for art, so overlapping refreshes must not stack duplicate runs.
	coverMu       sync.Mutex
	coverPrefetch map[string]struct{}
	log           *utils.Logger
}

// defaultScanTimeout is the scan bound when no timeout is configured.
const defaultScanTimeout = 10 * time.Minute

// NewRunner returns a Runner dispatching queued scan/import/prune work.
func NewRunner(st *store.Store, mb *musicbrainz.Client, l *utils.Logger, q *Queue,
	art *artwork.Fetcher, coverDir string,
) *Runner {
	return &Runner{
		queue:       q,
		st:          st,
		mb:          mb,
		art:         art,
		coverDir:    coverDir,
		scanTimeout: defaultScanTimeout,
		log:         l,
	}
}

// SetScanTimeout overrides the scan bound; non-positive values keep the default.
func (r *Runner) SetScanTimeout(d time.Duration) {
	if d > 0 {
		r.scanTimeout = d
	}
}

// scanTimeoutOrDefault returns the configured scan bound or the default.
func (r *Runner) scanTimeoutOrDefault() time.Duration {
	if r.scanTimeout > 0 {
		return r.scanTimeout
	}
	return defaultScanTimeout
}

// Start runs queued tasks serially until ctx is canceled, then waits for
// detached cover work so the store is never closed while it still runs.
func (r *Runner) Start(ctx context.Context) {
	r.baseCtx = ctx
	r.queue.run(ctx, r.exec)
	r.bgWg.Wait()
}

// detachedBase returns the context detached cover work derives from: the
// queue-run context when Start has set it, Background otherwise (tests
// driving exec directly).
func (r *Runner) detachedBase() context.Context {
	if r.baseCtx != nil {
		return r.baseCtx
	}
	return context.Background()
}

// shuttingDown reports whether the runner is stopping. Failures observed
// during shutdown are interruptions to resume on restart, not import
// failures to record.
func (r *Runner) shuttingDown() bool {
	return r.baseCtx != nil && r.baseCtx.Err() != nil
}

// spawnPrefetch runs fn detached from the finished import task with its own
// bound, canceling on runner shutdown. Start waits for all spawned runs.
func (r *Runner) spawnPrefetch(fn func(context.Context), timeout time.Duration) {
	if r.art == nil {
		return
	}
	bctx, cancel := context.WithTimeout(r.detachedBase(), timeout)
	r.bgWg.Add(1)
	go func() {
		defer cancel()
		defer r.bgWg.Done()
		fn(bctx)
	}()
}

// exec dispatches a task with per-kind timeouts (configurable scan, 30m import).
func (r *Runner) exec(ctx context.Context, t Task) {
	switch t.Key {
	case TaskScanRoots:
		sctx, cancel := context.WithTimeout(ctx, r.scanTimeoutOrDefault())
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

// scanRoots walks configured roots, syncs DB rows by path in streamed
// chunks, auto-links exact-MBID files (tag rewrite + move), prunes missing
// files, and sweeps orphan credits/collaborators. Roots whose walk is
// incomplete never prune; roots whose complete walk yields zero files keep
// their rows unless forcePrune is set. The walk streams into the sync in
// bounded chunks so large libraries never materialize; each chunk is one
// short write tx. Tag evidence (mb_track_id + tags_read_at) makes settled
// rescans skip tag parses, and the auto-link pass pages DB candidates
// instead of re-reading every file.
func (r *Runner) scanRoots(ctx context.Context, forcePrune bool) {
	r.log.Info("scanning root folders...")

	start := time.Now()
	scanStart := time.Now().UTC().Format("2006-01-02 15:04:05")
	r.queue.markScanRunning()

	roots := r.st.RootPaths()
	if len(roots) == 0 {
		auto := r.autoLinkMoves(ctx)
		r.cleanupCredits(ctx)
		r.pruneArtists(ctx)
		r.queue.markScanDone(ScanResult{
			AutoLinked:      auto.Linked,
			Moved:           auto.Moved,
			SkippedConflict: auto.SkippedConflict,
		}, time.Since(start))
		r.log.Infof("scan done: found=0 upserted=0 pruned=0 failed=0 kept_roots=0 autolinked=%d moved=%d skipped=%d in %s",
			auto.Linked, auto.Moved, auto.SkippedConflict, time.Since(start))
		return
	}

	r.cleanupStaleTemps(ctx)
	rootIDs, existingCounts, err := r.st.LoadSyncState(ctx)
	if err != nil {
		if ctx.Err() != nil || r.shuttingDown() {
			r.log.Warnf("scan interrupted by shutdown, will resume on restart: %v", err)
			return
		}
		r.queue.markScanFailed(err, time.Since(start))
		r.log.Errorf("scan failed in %s: %v", time.Since(start), err)
		return
	}

	foundCounts := make(map[string]int, len(rootIDs))
	var found, upserted, tagReads, unchanged int
	var tagReadMs int64
	var failed []string
	incomplete := []string{}

	walkStart := time.Now()
	chunk := make([]store.ScannedFile, 0, store.SyncChunkSize)
	flush := func() error {
		if len(chunk) == 0 {
			return nil
		}
		stats, err := r.st.SyncFileChunk(ctx, chunk, scanStart, rootIDs)
		if err != nil {
			return err
		}
		upserted += stats.Upserted
		tagReads += stats.TagReads
		unchanged += stats.Unchanged
		tagReadMs += stats.TagReadMs
		failed = append(failed, stats.Failed...)
		chunk = chunk[:0]
		return nil
	}
	var syncPhaseMs int64
	statuses, err := library.Scan(ctx, roots, func(f library.File) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		chunk = append(chunk, store.ScannedFile{
			LibraryRoot: f.LibraryRoot,
			Path:        f.Path,
			SizeBytes:   f.SizeBytes,
			MtimeNs:     f.MtimeNs,
			FormatHint:  f.FormatHint,
		})
		foundCounts[f.LibraryRoot]++
		found++
		if len(chunk) >= store.SyncChunkSize {
			t0 := time.Now()
			if ferr := flush(); ferr != nil {
				return ferr
			}
			syncPhaseMs += time.Since(t0).Milliseconds()
		}
		return nil
	})
	walkMs := time.Since(walkStart).Milliseconds()
	if err != nil {
		if ctx.Err() != nil || r.shuttingDown() {
			r.log.Warnf("scan interrupted by shutdown, will resume on restart: %v", err)
			return
		}
		r.queue.markScanFailed(err, time.Since(start))
		r.log.Errorf("scan failed in %s: %v", time.Since(start), err)
		return
	}
	for _, s := range statuses {
		if !s.Walked {
			r.log.Warnf("scan root=%q walk incomplete, skipping prune for its rows: %v", s.Root, s.WalkErr)
			incomplete = append(incomplete, s.Root)
		}
	}
	{
		t0 := time.Now()
		if err := flush(); err != nil {
			if ctx.Err() != nil || r.shuttingDown() {
				r.log.Warnf("scan interrupted by shutdown, will resume on restart: %v", err)
				return
			}
			r.queue.markScanFailed(err, time.Since(t0))
			r.log.Errorf("scan failed in %s: %v", time.Since(start), err)
			return
		}
		syncPhaseMs += time.Since(t0).Milliseconds()
	}

	pruneStart := time.Now()
	pruned, kept, err := r.st.PruneScanned(ctx, rootIDs, existingCounts, foundCounts, scanStart, store.SyncOptions{
		IncompleteRoots: incomplete,
		ForcePrune:      forcePrune,
	})
	if err != nil {
		if ctx.Err() != nil || r.shuttingDown() {
			r.log.Warnf("scan interrupted by shutdown, will resume on restart: %v", err)
			return
		}
		r.queue.markScanFailed(err, time.Since(start))
		r.log.Errorf("scan failed in %s: %v", time.Since(start), err)
		return
	}
	syncPhaseMs += time.Since(pruneStart).Milliseconds()

	autolinkStart := time.Now()
	auto := r.autoLinkMoves(ctx)
	autolinkMs := time.Since(autolinkStart).Milliseconds()
	r.cleanupCredits(ctx)
	r.pruneArtists(ctx)
	r.queue.markScanDone(ScanResult{
		Found:           found,
		Upserted:        upserted,
		Pruned:          pruned,
		AutoLinked:      auto.Linked,
		Moved:           auto.Moved,
		SkippedConflict: auto.SkippedConflict,
		Failed:          failed,
		KeptRoots:       kept,
		IncompleteRoots: incomplete,
		WalkMs:          walkMs,
		SyncMs:          syncPhaseMs,
		TagReadMs:       tagReadMs,
		AutolinkMs:      autolinkMs,
		TagReads:        tagReads,
		Unchanged:       unchanged,
	}, time.Since(start))
	r.log.Infof("scan done: found=%d upserted=%d pruned=%d failed=%d kept_roots=%d autolinked=%d moved=%d skipped=%d tag_reads=%d unchanged=%d walk_ms=%d sync_ms=%d tag_read_ms=%d autolink_ms=%d in %s",
		found, upserted, pruned, len(failed), len(kept), auto.Linked, auto.Moved, auto.SkippedConflict, tagReads, unchanged, walkMs, syncPhaseMs, tagReadMs, autolinkMs, time.Since(start))
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
		if r.shuttingDown() {
			r.log.Warnf("importing artist discography %q interrupted by shutdown, will resume on restart", t.ID)
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
		if r.shuttingDown() {
			r.log.Warnf("importing artist discography %q interrupted by shutdown, will resume on restart", t.ID)
			return
		}
		r.queue.markArtistImportFailed(t.ID, err)
		r.log.Errorf("importing artist discography %q failed in %s: %v", t.ID, time.Since(start), err)
		return
	}
	// Covers fill in in the background: the import is done as far as status
	// is concerned, and the serial task queue stays free for other work.
	// Gaps are always backfilled lazily on first GET .../cover. The runs
	// derive from the runner context (not the finished task) so they
	// survive the task but still cancel on shutdown; Start waits for them.
	r.spawnPrefetch(func(bctx context.Context) { r.prefetchCoversBG(bctx, t.ID) }, 30*time.Minute)
	r.spawnPrefetch(func(bctx context.Context) { r.prefetchArtistCover(bctx, t.ID, art.Name) }, 5*time.Minute)
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
