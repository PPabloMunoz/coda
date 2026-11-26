package tasks

import (
	"context"
	"sync"
	"time"

	"github.com/ppablomunoz/coda/internal/utils"
)

type Queue struct {
	mu            sync.Mutex
	items         []Task
	notify        chan struct{}
	artistImports map[string]*ArtistImportStatus
	log           *utils.Logger
}

// NewQueue returns an empty serial FIFO queue with artist-import tracking.
func NewQueue(log *utils.Logger) *Queue {
	return &Queue{
		items:         make([]Task, 0),
		notify:        make(chan struct{}, 1),
		artistImports: make(map[string]*ArtistImportStatus),
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

// run drains queued tasks serially via exec until ctx is canceled.
func (q *Queue) run(ctx context.Context, exec func(context.Context, Task)) {
	for {
		t, ok := q.pop()
		if ok {
			exec(ctx, t)
			continue
		}
		select {
		case <-q.notify:
		case <-ctx.Done():
			return
		}
	}
}

// EnqueueScan queues a library scan unless one is already pending.
func (q *Queue) EnqueueScan() {
	q.mu.Lock()
	for _, t := range q.items {
		if t.Key == TaskScanRoots {
			q.mu.Unlock()
			q.debug("task scan-roots already queued")
			return
		}
	}
	q.items = append(q.items, Task{Key: TaskScanRoots})
	depth := len(q.items)
	q.mu.Unlock()
	q.debugf("task scan-roots queued (depth=%d)", depth)
	q.signal()
}

// EnqueuePrune queues an orphan-collaborator prune unless one is already pending.
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

	now := time.Now().UTC()
	status := &ArtistImportStatus{
		ArtistID: id,
		State:    ArtistImportQueued,
		QueuedAt: &now,
	}
	q.artistImports[id] = status
	q.items = append(q.items, Task{Key: TaskImportDiscography, ID: id})
	depth := len(q.items)
	q.mu.Unlock()
	q.debugf("task import-discography queued %q (depth=%d)", id, depth)
	q.signal()

	return *status
}

// EnqueueDeleteInactiveArtists queues a hard-delete sweep unless one is already pending.
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
// run covers repeated uploads.
func (q *Queue) EnqueueEmbedReleaseCover(releaseID string) {
	q.mu.Lock()
	for _, t := range q.items {
		if t.Key == TaskEmbedReleaseCover && t.ID == releaseID {
			q.mu.Unlock()
			q.debugf("task %s already queued %q", TaskEmbedReleaseCover, releaseID)
			return
		}
	}
	q.items = append(q.items, Task{Key: TaskEmbedReleaseCover, ID: releaseID})
	depth := len(q.items)
	q.mu.Unlock()
	q.debugf("task %s queued %q (depth=%d)", TaskEmbedReleaseCover, releaseID, depth)
	q.signal()
}

// EnqueueClearReleaseCover queues a background wipe of the embedded front
// cover from the release's linked files after a cover removal. Dedupes per
// release while queued: the task re-checks the tombstone at run time, so
// one run covers repeated removals and an intervening upload wins.
func (q *Queue) EnqueueClearReleaseCover(releaseID string) {
	q.mu.Lock()
	for _, t := range q.items {
		if t.Key == TaskClearReleaseCover && t.ID == releaseID {
			q.mu.Unlock()
			q.debugf("task %s already queued %q", TaskClearReleaseCover, releaseID)
			return
		}
	}
	q.items = append(q.items, Task{Key: TaskClearReleaseCover, ID: releaseID})
	depth := len(q.items)
	q.mu.Unlock()
	q.debugf("task %s queued %q (depth=%d)", TaskClearReleaseCover, releaseID, depth)
	q.signal()
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

// markArtistImportRunning marks an import as running with a start timestamp.
func (q *Queue) markArtistImportRunning(id string) {
	q.mu.Lock()
	defer q.mu.Unlock()

	status, ok := q.artistImports[id]
	if !ok {
		return
	}
	now := time.Now().UTC()
	status.State = ArtistImportRunning
	status.StartedAt = &now
	status.FinishedAt = nil
	status.Error = ""
}

// markArtistImportDone marks an import as done with a finish timestamp.
func (q *Queue) markArtistImportDone(id string) {
	q.mu.Lock()
	defer q.mu.Unlock()

	status, ok := q.artistImports[id]
	if !ok {
		return
	}
	now := time.Now().UTC()
	status.State = ArtistImportDone
	status.FinishedAt = &now
	status.Error = ""
}

// ForgetArtistImport drops the status entry for id, so removed artists
// don't leave stale import state behind forever.
func (q *Queue) ForgetArtistImport(id string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	delete(q.artistImports, id)
}

// markArtistImportFailed marks an import as failed with the error message and finish timestamp.
func (q *Queue) markArtistImportFailed(id string, err error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	status, ok := q.artistImports[id]
	if !ok {
		return
	}
	now := time.Now().UTC()
	status.State = ArtistImportFailed
	status.FinishedAt = &now
	status.Error = err.Error()
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
