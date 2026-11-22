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

func (q *Queue) ArtistImportStatus(id string) ArtistImportStatus {
	q.mu.Lock()
	defer q.mu.Unlock()

	if status, ok := q.artistImports[id]; ok {
		return *status
	}
	return ArtistImportStatus{ArtistID: id, State: ArtistImportIdle}
}

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
