package tasks

import (
	"errors"
	"sync"
	"testing"
)

func TestEnqueueScanDedup(t *testing.T) {
	q := NewQueue(nil, nil)
	q.EnqueueScan(false)
	q.EnqueueScan(false)
	q.mu.Lock()
	n := 0
	for _, item := range q.items {
		if item.Key == TaskScanRoots {
			n++
		}
	}
	depth := len(q.items)
	q.mu.Unlock()
	if n != 1 {
		t.Errorf("scan tasks = %d, want 1 (depth=%d)", n, depth)
	}
}

func TestEnqueueScanForceUpgradesPending(t *testing.T) {
	q := NewQueue(nil, nil)
	q.EnqueueScan(false)
	q.EnqueueScan(true)
	q.mu.Lock()
	if len(q.items) != 1 {
		q.mu.Unlock()
		t.Fatalf("tasks = %d, want 1 (force upgrades, not duplicates)", len(q.items))
	}
	forced := q.items[0].ForcePrune
	q.mu.Unlock()
	if !forced {
		t.Error("pending scan not upgraded to force prune")
	}

	// Force first, then normal: stays forced, still a single task.
	q2 := NewQueue(nil, nil)
	q2.EnqueueScan(true)
	q2.EnqueueScan(false)
	q2.mu.Lock()
	if len(q2.items) != 1 || !q2.items[0].ForcePrune {
		q2.mu.Unlock()
		t.Errorf("force-then-normal should stay a single forced scan")
	} else {
		q2.mu.Unlock()
	}
}

func TestEnqueuePruneDedup(t *testing.T) {
	q := NewQueue(nil, nil)
	q.EnqueuePrune()
	q.EnqueuePrune()
	q.mu.Lock()
	n := 0
	for _, item := range q.items {
		if item.Key == TaskPruneArtists {
			n++
		}
	}
	q.mu.Unlock()
	if n != 1 {
		t.Errorf("prune tasks = %d, want 1", n)
	}
}

func TestEnqueueRefreshArtistDedup(t *testing.T) {
	q := NewQueue(nil, nil)
	first := q.EnqueueRefreshArtist("artist-1")
	if first.State != ArtistImportQueued {
		t.Errorf("state = %q, want queued", first.State)
	}
	if first.ArtistID != "artist-1" {
		t.Errorf("artist = %q", first.ArtistID)
	}
	second := q.EnqueueRefreshArtist("artist-1")
	if second.State != ArtistImportQueued {
		t.Errorf("second state = %q, want queued (dedup)", second.State)
	}

	q.markArtistImportRunning("artist-1")
	third := q.EnqueueRefreshArtist("artist-1")
	if third.State != ArtistImportRunning {
		t.Errorf("running dedup state = %q, want running", third.State)
	}

	// Different artist queues independently.
	other := q.EnqueueRefreshArtist("artist-2")
	if other.State != ArtistImportQueued {
		t.Errorf("other state = %q, want queued", other.State)
	}
}

func TestArtistImportStatusIdle(t *testing.T) {
	q := NewQueue(nil, nil)
	got := q.ArtistImportStatus("missing")
	if got.State != ArtistImportIdle {
		t.Errorf("state = %q, want idle", got.State)
	}
	if got.ArtistID != "missing" {
		t.Errorf("artist = %q", got.ArtistID)
	}
}

func TestMarkTransitions(t *testing.T) {
	q := NewQueue(nil, nil)
	q.EnqueueRefreshArtist("a1")

	q.markArtistImportRunning("a1")
	if got := q.ArtistImportStatus("a1"); got.State != ArtistImportRunning {
		t.Errorf("state = %q, want running", got.State)
	} else {
		if got.StartedAt == nil {
			t.Error("StartedAt nil after running")
		}
		if got.FinishedAt != nil {
			t.Error("FinishedAt set while running")
		}
	}

	q.markArtistImportDone("a1")
	if got := q.ArtistImportStatus("a1"); got.State != ArtistImportDone {
		t.Errorf("state = %q, want done", got.State)
	} else if got.FinishedAt == nil {
		t.Error("FinishedAt nil after done")
	}

	// Re-queue after done is allowed.
	if got := q.EnqueueRefreshArtist("a1"); got.State != ArtistImportQueued {
		t.Errorf("requeue after done = %q, want queued", got.State)
	}

	q.markArtistImportRunning("a1")
	q.markArtistImportFailed("a1", errors.New("boom"))
	got := q.ArtistImportStatus("a1")
	if got.State != ArtistImportFailed {
		t.Errorf("state = %q, want failed", got.State)
	}
	if got.Error != "boom" {
		t.Errorf("error = %q, want boom", got.Error)
	}
}

func TestMarkUnknownNoPanic(t *testing.T) {
	q := NewQueue(nil, nil)
	q.markArtistImportRunning("nope")
	q.markArtistImportDone("nope")
	q.markArtistImportFailed("nope", errors.New("x"))
}

func TestWithCoverLockSerializesSameRelease(t *testing.T) {
	const goroutines, perGoroutine = 8, 25

	// Deliberately unlocked: only WithReleaseCoverLock's serialization makes
	// the read-modify-write lossless. Lost updates mean the lock leaked.
	counter := 0
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				if err := WithReleaseCoverLock("release-1", func() error {
					counter++
					return nil
				}); err != nil {
					t.Errorf("WithReleaseCoverLock: %v", err)
				}
			}
		}()
	}
	wg.Wait()
	if counter != goroutines*perGoroutine {
		t.Errorf("counter = %d, want %d (lost updates mean the per-release lock leaked)",
			counter, goroutines*perGoroutine)
	}
}

func TestPrefetchTasksAreLowPriority(t *testing.T) {
	q := NewQueue(nil, nil)
	q.EnqueuePrefetchCover("rel-1")
	q.EnqueuePrefetchArtistCover("artist-1")
	q.EnqueueScan(false)
	q.EnqueueRefreshArtist("artist-2")

	// Normal tasks first, in FIFO order; prefetch drains afterwards.
	want := []Task{
		{Key: TaskScanRoots},
		{Key: TaskImportDiscography, ID: "artist-2"},
		{Key: TaskPrefetchCover, ID: "rel-1"},
		{Key: TaskPrefetchArtistCover, ID: "artist-1"},
	}
	for i, w := range want {
		got, ok := q.pop()
		if !ok {
			t.Fatalf("pop %d: queue empty, want %s %q", i, w.Key, w.ID)
		}
		if got.Key != w.Key || got.ID != w.ID {
			t.Errorf("task[%d] = %s %q, want %s %q", i, got.Key, got.ID, w.Key, w.ID)
		}
	}
	if _, ok := q.pop(); ok {
		t.Error("pop after drain returned a task, want empty")
	}
}

func TestPrefetchDedupe(t *testing.T) {
	q := NewQueue(nil, nil)
	q.EnqueuePrefetchCover("rel-1")
	q.EnqueuePrefetchCover("rel-1")
	q.EnqueuePrefetchArtistCover("a1")
	q.EnqueuePrefetchArtistCover("a1")
	q.EnqueuePrefetchCover("rel-2")

	if !q.HasTask(TaskPrefetchCover, "rel-1") {
		t.Error("HasTask prefetch cover rel-1 = false, want true")
	}
	q.mu.Lock()
	low := append([]Task(nil), q.low...)
	q.mu.Unlock()
	count := map[Task]int{}
	for _, item := range low {
		count[item]++
	}
	if count[Task{Key: TaskPrefetchCover, ID: "rel-1"}] != 1 {
		t.Errorf("prefetch cover rel-1 queued %d times, want 1 (dedup)",
			count[Task{Key: TaskPrefetchCover, ID: "rel-1"}])
	}
	if count[Task{Key: TaskPrefetchArtistCover, ID: "a1"}] != 1 {
		t.Errorf("prefetch artist a1 queued %d times, want 1 (dedup)",
			count[Task{Key: TaskPrefetchArtistCover, ID: "a1"}])
	}
	if len(low) != 3 {
		t.Errorf("low queue = %d tasks, want 3 (rel-1 + a1 + rel-2)", len(low))
	}
}
