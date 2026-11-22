package tasks

import (
	"context"
	"time"

	apperrors "github.com/ppablomunoz/coda/internal/errors"
	"github.com/ppablomunoz/coda/internal/library"
	"github.com/ppablomunoz/coda/internal/musicbrainz"
	"github.com/ppablomunoz/coda/internal/store"
	"github.com/ppablomunoz/coda/internal/utils"
)

type Runner struct {
	queue *Queue
	st    *store.Store
	mb    *musicbrainz.Client // same instance as server, don't make two (rate limiter is per-client)
	log   *utils.Logger
}

func NewRunner(st *store.Store, mb *musicbrainz.Client, l *utils.Logger, q *Queue) *Runner {
	return &Runner{
		queue: q,
		st:    st,
		mb:    mb,
		log:   l,
	}
}

func (r *Runner) Start(ctx context.Context) {
	r.queue.run(ctx, r.exec)
}

func (r *Runner) exec(ctx context.Context, t Task) {
	switch t.Key {
	case TaskScanRoots:
		sctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		defer cancel()
		r.scanRoots(sctx)
	case TaskImportDiscography:
		sctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
		defer cancel()
		r.importArtist(sctx, t)
	case TaskPruneArtists:
		r.pruneArtists(ctx)
	case TaskDeleteInactiveArtists:
		r.deleteInactiveArtists(ctx)
	default:
		r.log.Warnf("task unknown key %q, skipping", t.Key)
	}
}

func (r *Runner) scanRoots(ctx context.Context) {
	r.log.Info("scanning root folders...")

	start := time.Now()
	scanStart := time.Now().UTC().Format("2006-01-02 15:04:05")

	roots := r.st.RootPaths()
	files := []store.ScannedFile{}
	if len(roots) > 0 {
		if err := library.Scan(ctx, roots, func(f library.File) error {
			files = append(files, store.ScannedFile{
				LibraryRoot: f.LibraryRoot,
				Path:        f.Path,
				SizeBytes:   f.SizeBytes,
				MtimeNs:     f.MtimeNs,
				FormatHint:  f.FormatHint,
			})
			return nil
		}); err != nil {
			r.log.Errorf("scan failed in %s: %v", time.Since(start), err)
			return
		}
	}

	stats, err := r.st.SyncLocalFiles(ctx, files, scanStart)
	if err != nil {
		r.log.Errorf("scan failed in %s: %v", time.Since(start), err)
		return
	}
	r.cleanupCredits(ctx)
	r.pruneArtists(ctx)
	r.log.Infof("scan done: found=%d upserted=%d pruned=%d in %s",
		stats.Found, stats.Upserted, stats.Pruned, time.Since(start))
}

func (r *Runner) importArtist(ctx context.Context, t Task) {
	if t.ID == "" {
		r.log.Warn("task import-discography missing artist id, skipping")
		return
	}

	start := time.Now()
	r.log.Infof("importing artist discography %q", t.ID)
	sctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()

	r.queue.markArtistImportRunning(t.ID)
	art, err := r.st.GetArtistByID(ctx, t.ID)
	if err != nil {
		if apperrors.Is(err, apperrors.ErrNotFound) {
			r.log.Infof("skipping discography import for inactive artist %q", t.ID)
			r.queue.markArtistImportDone(t.ID)
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
	if err := r.importDiscography(sctx, t.ID); err != nil {
		r.queue.markArtistImportFailed(t.ID, err)
		r.log.Errorf("importing artist discography %q failed in %s: %v", t.ID, time.Since(start), err)
		return
	}
	r.cleanupCredits(sctx)
	r.pruneArtists(sctx)
	r.queue.markArtistImportDone(t.ID)
	r.log.Infof("discography for %q saved in %s", t.ID, time.Since(start))
}

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
