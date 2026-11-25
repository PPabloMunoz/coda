package tasks

import (
	"errors"
	"sync"
	"testing"
)

func TestEnqueueScanDedup(t *testing.T) {
	q := NewQueue(nil)
	q.EnqueueScan()
	q.EnqueueScan()
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

func TestEnqueuePruneDedup(t *testing.T) {
	q := NewQueue(nil)
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
	q := NewQueue(nil)
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
	q := NewQueue(nil)
	got := q.ArtistImportStatus("missing")
	if got.State != ArtistImportIdle {
		t.Errorf("state = %q, want idle", got.State)
	}
	if got.ArtistID != "missing" {
		t.Errorf("artist = %q", got.ArtistID)
	}
}

func TestMarkTransitions(t *testing.T) {
	q := NewQueue(nil)
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
	q := NewQueue(nil)
	q.markArtistImportRunning("nope")
	q.markArtistImportDone("nope")
	q.markArtistImportFailed("nope", errors.New("x"))
}

func TestWithCoverLockSerializesSameRelease(t *testing.T) {
	const goroutines, perGoroutine = 8, 25

	// Deliberately unlocked: only withCoverLock's serialization makes the
	// read-modify-write lossless. Lost updates mean the lock leaked.
	counter := 0
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				if err := withCoverLock("release-1", func() error {
					counter++
					return nil
				}); err != nil {
					t.Errorf("withCoverLock: %v", err)
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
