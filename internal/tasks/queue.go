package tasks

import (
	"context"
	"sync"
	"time"

	"github.com/ppablomunoz/coda/internal/store"
	"github.com/ppablomunoz/coda/internal/utils"
)

type Queue struct {
	mu            sync.Mutex
	items         []Task
	notify        chan struct{}
	artistImports map[string]*ArtistImportStatus
	// scan is the observable library-scan lifecycle. Queued/running live
	// here only; terminal states (done/failed) also persist via scan_runs
	// so the last run survives a restart.
	scan *ScanStatus
	st   *store.Store // nil disables persistence (tests)
	log  *utils.Logger
}

// NewQueue returns an empty serial FIFO queue with artist-import tracking.
// When st is non-nil, import lifecycles and pending release file jobs are
// persisted best-effort so a restart can recover them via Recover; a nil
// store keeps the previous memory-only behavior.
func NewQueue(log *utils.Logger, st *store.Store) *Queue {
	return &Queue{
		items:         make([]Task, 0),
		notify:        make(chan struct{}, 1),
		artistImports: make(map[string]*ArtistImportStatus),
		st:            st,
		log:           log,
	}
}

func (q *Queue) signal() {
	select {
	case q.notify <- struct{}{}:
	default:
	}
}

// pop gets the first element in the queue
// and move the queue one ahead. If there is no
// item, return false
func (q *Queue) pop() (Task, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		return Task{}, false
	}
	item := q.items[0]
	q.items = q.items[1:]
	return item, true
}

// run drains queued tasks serially via exec until ctx is canceled. Pending
// release file jobs are deleted from the store after a clean run; when ctx
// is canceled (shutdown) the row is kept so the next startup recovers it.
// Imports persist via the artist_imports table instead (see Recover).
func (q *Queue) run(ctx context.Context, exec func(context.Context, Task)) {
	for {
		t, ok := q.pop()
		if ok {
			exec(ctx, t)
			q.afterExec(ctx, t)
			continue
		}
		select {
		case <-q.notify:
		case <-ctx.Done():
			return
		}
	}
}

// afterExec drops the durable intent for a finished file job. A shutdown
// interrupt keeps the row for recovery, as does a duplicate that was
// re-enqueued while the task was running (the next run covers it).
func (q *Queue) afterExec(ctx context.Context, t Task) {
	if q.st == nil {
		return
	}
	kind, ok := pendingKindForTask(t)
	if !ok {
		return
	}
	if ctx.Err() != nil {
		return
	}
	q.mu.Lock()
	pending := false
	for _, item := range q.items {
		if item.Key == t.Key && item.ID == t.ID {
			pending = true
			break
		}
	}
	q.mu.Unlock()
	if pending {
		return
	}
	if err := q.st.DeletePendingReleaseTask(context.Background(), t.ID, kind); err != nil {
		q.debugf("task %s %q persist delete: %v", t.Key, t.ID, err)
	}
}

// EnqueueScan queues a library scan unless one is already pending. A pending
// scan is upgraded to a force prune when force is requested.
// ForcePrune allows pruning roots whose complete walk yields zero files;
// incomplete walks never prune. The scan status moves to queued unless a
// scan is already running (the running run stays visible until it finishes;
// the newly queued run marks itself queued when it starts). Scans are
// periodic (plus one at startup), so queued work is not persisted: a restart
// simply scans again, while the last completed run hydrates via Recover.
func (q *Queue) EnqueueScan(force bool) {
	q.mu.Lock()
	for i, t := range q.items {
		if t.Key == TaskScanRoots {
			if force && !q.items[i].ForcePrune {
				q.items[i].ForcePrune = true
			}
			q.mu.Unlock()
			q.debug("task scan-roots already queued")
			return
		}
	}
	q.items = append(q.items, Task{Key: TaskScanRoots, ForcePrune: force})
	depth := len(q.items)
	if q.scan == nil || q.scan.State != ScanRunning {
		// Fresh lifecycle timestamps, but keep the last completed run's
		// counters until the new run finishes (zero when no run has
		// completed yet).
		now := time.Now().UTC()
		if q.scan == nil {
			q.scan = &ScanStatus{}
		}
		q.scan.State = ScanQueued
		q.scan.QueuedAt = &now
		q.scan.StartedAt = nil
		q.scan.FinishedAt = nil
		q.scan.DurationMs = 0
		q.scan.Error = ""
	}
	q.mu.Unlock()
	q.debugf("task scan-roots queued (depth=%d force=%v)", depth, force)
	q.signal()
}

// EnqueuePrune queues an orphan-collaborator prune unless one is already pending.
// Prunes run on a daily tick, so they are not persisted across restarts.
func (q *Queue) EnqueuePrune() {
	q.mu.Lock()
	for _, t := range q.items {
		if t.Key == TaskPruneArtists {
			q.mu.Unlock()
			q.debugf("task %s already queued", TaskPruneArtists)
			return
		}
	}
	q.items = append(q.items, Task{Key: TaskPruneArtists})
	depth := len(q.items)
	q.mu.Unlock()
	q.debugf("task prune-artists queued (depth=%d)", depth)
	q.signal()
}

// EnqueueRefreshArtist queues a discography import, deduping while queued/running.
// The queued lifecycle is persisted so a restart recovers instead of
// silently resetting the status to idle.
func (q *Queue) EnqueueRefreshArtist(id string) ArtistImportStatus {
	q.mu.Lock()
	if status, ok := q.artistImports[id]; ok {
		if status.State == ArtistImportQueued || status.State == ArtistImportRunning {
			current := *status
			q.mu.Unlock()
			q.debugf("task import-discography already %s %q", current.State, id)
			return current
		}
	}
	q.mu.Unlock()

	now := time.Now().UTC()
	status := &ArtistImportStatus{
		ArtistID: id,
		State:    ArtistImportQueued,
		QueuedAt: &now,
	}
	q.persistImport(*status)

	q.mu.Lock()
	// Re-check under the lock: a concurrent enqueue may have won while the
	// DB write was in flight. The persisted row already covers either
	// winner, so the loser just reports the winner's state.
	if cur, ok := q.artistImports[id]; ok &&
		(cur.State == ArtistImportQueued || cur.State == ArtistImportRunning) {
		current := *cur
		q.mu.Unlock()
		q.debugf("task import-discography already %s %q", current.State, id)
		return current
	}
	q.artistImports[id] = status
	q.items = append(q.items, Task{Key: TaskImportDiscography, ID: id})
	depth := len(q.items)
	current := *status
	q.mu.Unlock()
	q.debugf("task import-discography queued %q (depth=%d)", id, depth)
	q.signal()

	return current
}

// EnqueueDeleteInactiveArtists queues a hard-delete sweep unless one is already pending.
// The sweep runs weekly, so it is not persisted across restarts.
func (q *Queue) EnqueueDeleteInactiveArtists() {
	q.mu.Lock()
	for _, t := range q.items {
		if t.Key == TaskDeleteInactiveArtists {
			q.mu.Unlock()
			q.debugf("task %s already queued", TaskDeleteInactiveArtists)
			return
		}
	}
	q.items = append(q.items, Task{Key: TaskDeleteInactiveArtists})
	depth := len(q.items)
	q.mu.Unlock()
	q.debugf("task %s queued (depth=%d)", TaskDeleteInactiveArtists, depth)
	q.signal()
}

// EnqueueEmbedReleaseCover queues a background re-embed of the cached
// release cover into its linked files. Dedupes per release while queued:
// the task always embeds whatever bytes are cached at run time, so one
// run covers repeated uploads. The intent is persisted so a restart
// re-embeds instead of leaving files stale.
func (q *Queue) EnqueueEmbedReleaseCover(releaseID string) {
	q.enqueueReleaseTask(TaskEmbedReleaseCover, releaseID, store.PendingTaskEmbed)
}

// EnqueueClearReleaseCover queues a background wipe of the embedded front
// cover from the release's linked files after a cover removal. Dedupes per
// release while queued: the task re-checks the tombstone at run time, so
// one run covers repeated removals and an intervening upload wins. The
// intent is persisted so a restart still clears the files.
func (q *Queue) EnqueueClearReleaseCover(releaseID string) {
	q.enqueueReleaseTask(TaskClearReleaseCover, releaseID, store.PendingTaskClear)
}

// EnqueueRelinkRelease queues a background tag/move rewrite for every file
// linked to a release after a release switch. Dedupes per release while
// queued: one run covers repeated switches, and the DB link is already
// remapped synchronously in ReplaceRelease so this only heals on-disk state.
// The intent is persisted so a restart still heals the files.
func (q *Queue) EnqueueRelinkRelease(releaseID string) {
	q.enqueueReleaseTask(TaskRelinkRelease, releaseID, store.PendingTaskRelink)
}

// enqueueReleaseTask dedupes in memory, persists the intent, and queues one
// run. The persisted row coalesces repeats (runtime state wins), while the
// in-memory queue preserves the current dedup behavior for live tasks.
func (q *Queue) enqueueReleaseTask(key TaskKey, releaseID, kind string) {
	q.mu.Lock()
	for _, t := range q.items {
		if t.Key == key && t.ID == releaseID {
			q.mu.Unlock()
			q.debugf("task %s already queued %q", key, releaseID)
			return
		}
	}
	q.mu.Unlock()

	if q.st != nil {
		if err := q.st.InsertPendingReleaseTask(context.Background(), releaseID, kind); err != nil {
			q.debugf("task %s %q persist: %v", key, releaseID, err)
		}
	}

	q.mu.Lock()
	for _, t := range q.items {
		if t.Key == key && t.ID == releaseID {
			q.mu.Unlock()
			q.debugf("task %s already queued %q", key, releaseID)
			return
		}
	}
	q.items = append(q.items, Task{Key: key, ID: releaseID})
	depth := len(q.items)
	q.mu.Unlock()
	q.debugf("task %s queued %q (depth=%d)", key, releaseID, depth)
	q.signal()
}

// HasTask reports whether a task with the given key and id is currently queued.
func (q *Queue) HasTask(key TaskKey, id string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, t := range q.items {
		if t.Key == key && t.ID == id {
			return true
		}
	}
	return false
}

// Recover re-enqueues durable intent after a restart: interrupted imports
// (queued/running) go back on the queue as queued, pending release file
// jobs return in FIFO order, and terminal import states (done/failed) are
// hydrated so the status is not silently reset to idle. The last completed
// scan run hydrates too (queued/running states are never persisted, so an
// interrupted scan simply resumes via the startup scan). File jobs are
// enqueued before imports: they finish in seconds while an import can hold
// the serial queue for up to 30 minutes.
func (q *Queue) Recover(ctx context.Context) error {
	if q.st == nil {
		return nil
	}

	if run, err := q.st.GetScanRun(ctx); err == nil {
		q.mu.Lock()
		q.scan = scanStatusFromRow(run)
		q.mu.Unlock()
	} else {
		q.debugf("recover: no completed scan run yet (%v)", err)
	}

	pending, err := q.st.ListPendingReleaseTasks(ctx)
	if err != nil {
		return err
	}
	imports, err := q.st.ListArtistImports(ctx)
	if err != nil {
		return err
	}

	recoveredFiles := 0
	for _, p := range pending {
		key, ok := taskKeyForPendingKind(p.Kind)
		if !ok {
			q.debugf("recover: unknown pending kind %q for release %q, skipping", p.Kind, p.ReleaseID)
			continue
		}
		q.mu.Lock()
		dup := false
		for _, item := range q.items {
			if item.Key == key && item.ID == p.ReleaseID {
				dup = true
				break
			}
		}
		if !dup {
			q.items = append(q.items, Task{Key: key, ID: p.ReleaseID})
			recoveredFiles++
		}
		q.mu.Unlock()
	}

	recoveredImports := 0
	for _, imp := range imports {
		status := importStatusFromRow(imp)
		switch status.State {
		case ArtistImportQueued, ArtistImportRunning:
			// An interrupted run goes back on the queue as queued,
			// preserving the original queue time when known.
			queued := status
			queued.State = ArtistImportQueued
			queued.StartedAt = nil
			queued.FinishedAt = nil
			queued.Error = ""
			q.persistImport(queued)
			q.mu.Lock()
			q.artistImports[imp.ArtistID] = &queued
			dup := false
			for _, item := range q.items {
				if item.Key == TaskImportDiscography && item.ID == imp.ArtistID {
					dup = true
					break
				}
			}
			if !dup {
				q.items = append(q.items, Task{Key: TaskImportDiscography, ID: imp.ArtistID})
				recoveredImports++
			}
			q.mu.Unlock()
		case ArtistImportDone, ArtistImportFailed:
			q.mu.Lock()
			cp := status
			q.artistImports[imp.ArtistID] = &cp
			q.mu.Unlock()
		default:
			q.debugf("recover: unknown import state %q for artist %q, skipping", imp.State, imp.ArtistID)
		}
	}

	if recoveredFiles > 0 || recoveredImports > 0 {
		q.signal()
	}
	if q.log != nil {
		q.log.Infof("recovered %d pending file tasks and %d interrupted imports",
			recoveredFiles, recoveredImports)
	}
	return nil
}

// ArtistImportStatus returns the current import state for id, or idle when unknown.
func (q *Queue) ArtistImportStatus(id string) ArtistImportStatus {
	q.mu.Lock()
	defer q.mu.Unlock()

	if status, ok := q.artistImports[id]; ok {
		return *status
	}
	return ArtistImportStatus{ArtistID: id, State: ArtistImportIdle}
}

// ScanStatus returns the current scan lifecycle, or idle when no scan has
// ever been queued.
func (q *Queue) ScanStatus() ScanStatus {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.scan != nil {
		return *q.scan
	}
	return ScanStatus{State: ScanIdle}
}

// markScanRunning moves the scan to running with a start timestamp. A scan
// re-queued behind a running one keeps showing running until the current
// run finishes; the next run re-marks itself when it starts.
func (q *Queue) markScanRunning() {
	q.mu.Lock()
	now := time.Now().UTC()
	if q.scan == nil {
		q.scan = &ScanStatus{}
	}
	q.scan.State = ScanRunning
	q.scan.StartedAt = &now
	q.scan.FinishedAt = nil
	q.scan.DurationMs = 0
	q.scan.Error = ""
	q.mu.Unlock()
}

// ScanResult carries the counters a finished scan reports.
type ScanResult struct {
	Found           int
	Upserted        int
	Pruned          int64
	AutoLinked      int
	Moved           int
	SkippedConflict int
	Failed          []string
	KeptRoots       []string
	IncompleteRoots []string
}

// markScanDone records a completed scan with its counters and persists the
// run so it survives a restart.
func (q *Queue) markScanDone(res ScanResult, duration time.Duration) {
	q.mu.Lock()
	now := time.Now().UTC()
	if q.scan == nil {
		q.scan = &ScanStatus{}
	}
	failed := append([]string(nil), res.Failed...)
	if len(failed) > MaxScanFailedFiles {
		failed = failed[:MaxScanFailedFiles]
	}
	q.scan.State = ScanDone
	q.scan.FinishedAt = &now
	q.scan.DurationMs = duration.Milliseconds()
	q.scan.Found = res.Found
	q.scan.Upserted = res.Upserted
	q.scan.Pruned = res.Pruned
	q.scan.AutoLinked = res.AutoLinked
	q.scan.Moved = res.Moved
	q.scan.SkippedConflict = res.SkippedConflict
	q.scan.FailedCount = len(res.Failed)
	q.scan.FailedFiles = failed
	q.scan.KeptRoots = append([]string(nil), res.KeptRoots...)
	q.scan.IncompleteRoots = append([]string(nil), res.IncompleteRoots...)
	q.scan.Error = ""
	cp := *q.scan
	q.mu.Unlock()
	q.persistScan(cp)
}

// markScanFailed records a failed scan with the error message and persists
// the run so the failure survives a restart. Counters reset: a failed run
// produced no reliable counts.
func (q *Queue) markScanFailed(err error, duration time.Duration) {
	if err == nil {
		return
	}
	q.mu.Lock()
	now := time.Now().UTC()
	if q.scan == nil {
		q.scan = &ScanStatus{}
	}
	q.scan.State = ScanFailed
	q.scan.FinishedAt = &now
	q.scan.DurationMs = duration.Milliseconds()
	q.scan.Found = 0
	q.scan.Upserted = 0
	q.scan.Pruned = 0
	q.scan.AutoLinked = 0
	q.scan.Moved = 0
	q.scan.SkippedConflict = 0
	q.scan.FailedCount = 0
	q.scan.FailedFiles = nil
	q.scan.KeptRoots = nil
	q.scan.IncompleteRoots = nil
	q.scan.Error = err.Error()
	cp := *q.scan
	q.mu.Unlock()
	q.persistScan(cp)
}

// persistScan mirrors a completed scan to the store. Best-effort: a
// persistence failure only logs; the in-memory status keeps working and
// the next completed scan retries.
func (q *Queue) persistScan(status ScanStatus) {
	if q.st == nil {
		return
	}
	if err := q.st.UpsertScanRun(context.Background(), store.ScanRun{
		State:           string(status.State),
		QueuedAt:        status.QueuedAt,
		StartedAt:       status.StartedAt,
		FinishedAt:      status.FinishedAt,
		DurationMs:      status.DurationMs,
		Found:           status.Found,
		Upserted:        status.Upserted,
		Pruned:          status.Pruned,
		AutoLinked:      status.AutoLinked,
		Moved:           status.Moved,
		SkippedConflict: status.SkippedConflict,
		FailedCount:     status.FailedCount,
		FailedFiles:     status.FailedFiles,
		KeptRoots:       status.KeptRoots,
		IncompleteRoots: status.IncompleteRoots,
		Error:           status.Error,
	}); err != nil {
		q.debugf("scan persist %s: %v", status.State, err)
	}
}

// scanStatusFromRow converts a stored scan run to its queue status.
func scanStatusFromRow(run *store.ScanRun) *ScanStatus {
	return &ScanStatus{
		State:           ScanState(run.State),
		QueuedAt:        run.QueuedAt,
		StartedAt:       run.StartedAt,
		FinishedAt:      run.FinishedAt,
		DurationMs:      run.DurationMs,
		Found:           run.Found,
		Upserted:        run.Upserted,
		Pruned:          run.Pruned,
		AutoLinked:      run.AutoLinked,
		Moved:           run.Moved,
		SkippedConflict: run.SkippedConflict,
		FailedCount:     run.FailedCount,
		FailedFiles:     append([]string(nil), run.FailedFiles...),
		KeptRoots:       append([]string(nil), run.KeptRoots...),
		IncompleteRoots: append([]string(nil), run.IncompleteRoots...),
		Error:           run.Error,
	}
}

// markArtistImportRunning marks an import as running with a start timestamp.
func (q *Queue) markArtistImportRunning(id string) {
	q.mu.Lock()
	status, ok := q.artistImports[id]
	if !ok {
		q.mu.Unlock()
		return
	}
	now := time.Now().UTC()
	status.State = ArtistImportRunning
	status.StartedAt = &now
	status.FinishedAt = nil
	status.Error = ""
	cp := *status
	q.mu.Unlock()
	q.persistImport(cp)
}

// markArtistImportDone marks an import as done with a finish timestamp.
func (q *Queue) markArtistImportDone(id string) {
	q.mu.Lock()
	status, ok := q.artistImports[id]
	if !ok {
		q.mu.Unlock()
		return
	}
	now := time.Now().UTC()
	status.State = ArtistImportDone
	status.FinishedAt = &now
	status.Error = ""
	cp := *status
	q.mu.Unlock()
	q.persistImport(cp)
}

// ForgetArtistImport drops the status entry for id, so removed artists
// don't leave stale import state behind forever.
func (q *Queue) ForgetArtistImport(id string) {
	q.mu.Lock()
	delete(q.artistImports, id)
	q.mu.Unlock()
	if q.st != nil {
		if err := q.st.DeleteArtistImport(context.Background(), id); err != nil {
			q.debugf("forget import %q persist delete: %v", id, err)
		}
	}
}

// markArtistImportFailed marks an import as failed with the error message and finish timestamp.
func (q *Queue) markArtistImportFailed(id string, err error) {
	q.mu.Lock()
	status, ok := q.artistImports[id]
	if !ok {
		q.mu.Unlock()
		return
	}
	now := time.Now().UTC()
	status.State = ArtistImportFailed
	status.FinishedAt = &now
	status.Error = err.Error()
	cp := *status
	q.mu.Unlock()
	q.persistImport(cp)
}

// persistImport mirrors an import lifecycle change to the store.
// Best-effort: a persistence failure only logs; the in-memory queue keeps
// working and the next transition retries.
func (q *Queue) persistImport(status ArtistImportStatus) {
	if q.st == nil {
		return
	}
	if err := q.st.UpsertArtistImport(context.Background(), store.ArtistImport{
		ArtistID:   status.ArtistID,
		State:      string(status.State),
		QueuedAt:   status.QueuedAt,
		StartedAt:  status.StartedAt,
		FinishedAt: status.FinishedAt,
		Error:      status.Error,
	}); err != nil {
		q.debugf("import %q persist %s: %v", status.ArtistID, status.State, err)
	}
}

// pendingKindForTask maps a file job task to its durable kind.
func pendingKindForTask(t Task) (string, bool) {
	switch t.Key {
	case TaskEmbedReleaseCover:
		return store.PendingTaskEmbed, true
	case TaskClearReleaseCover:
		return store.PendingTaskClear, true
	case TaskRelinkRelease:
		return store.PendingTaskRelink, true
	default:
		return "", false
	}
}

// taskKeyForPendingKind maps a durable kind back to its task key.
func taskKeyForPendingKind(kind string) (TaskKey, bool) {
	switch kind {
	case store.PendingTaskEmbed:
		return TaskEmbedReleaseCover, true
	case store.PendingTaskClear:
		return TaskClearReleaseCover, true
	case store.PendingTaskRelink:
		return TaskRelinkRelease, true
	default:
		return "", false
	}
}

// importStatusFromRow converts a stored import row to its queue status.
func importStatusFromRow(imp store.ArtistImport) ArtistImportStatus {
	return ArtistImportStatus{
		ArtistID:   imp.ArtistID,
		State:      ArtistImportState(imp.State),
		QueuedAt:   imp.QueuedAt,
		StartedAt:  imp.StartedAt,
		FinishedAt: imp.FinishedAt,
		Error:      imp.Error,
	}
}

func (q *Queue) debug(msg string) {
	if q.log != nil {
		q.log.Debug(msg)
	}
}

func (q *Queue) debugf(format string, args ...any) {
	if q.log != nil {
		q.log.Debugf(format, args...)
	}
}
