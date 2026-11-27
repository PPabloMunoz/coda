package tasks

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/ppablomunoz/coda/internal/store"
	"github.com/ppablomunoz/coda/internal/utils"
)

func TestScanStatusIdle(t *testing.T) {
	q := NewQueue(nil, nil)
	got := q.ScanStatus()
	if got.State != ScanIdle {
		t.Errorf("state = %q, want idle", got.State)
	}
}

func TestScanTransitions(t *testing.T) {
	q := NewQueue(nil, nil)

	q.EnqueueScan(false)
	if got := q.ScanStatus(); got.State != ScanQueued {
		t.Fatalf("state = %q, want queued", got.State)
	} else if got.QueuedAt == nil {
		t.Fatal("QueuedAt nil after enqueue")
	}

	// A second enqueue while queued keeps the queued state (dedup).
	first := q.ScanStatus()
	q.EnqueueScan(true)
	if got := q.ScanStatus(); got.State != ScanQueued {
		t.Errorf("state = %q, want queued after dedup", got.State)
	} else if !got.QueuedAt.Equal(*first.QueuedAt) {
		t.Error("QueuedAt changed on deduped enqueue")
	}

	// The runner picks the scan up.
	if _, ok := q.pop(); !ok {
		t.Fatal("pop scan task = false, want true")
	}
	q.markScanRunning()
	if got := q.ScanStatus(); got.State != ScanRunning {
		t.Fatalf("state = %q, want running", got.State)
	} else {
		if got.StartedAt == nil {
			t.Error("StartedAt nil while running")
		}
		if got.FinishedAt != nil {
			t.Error("FinishedAt set while running")
		}
	}

	// Enqueueing while running keeps the running state visible; the
	// pending run re-marks itself when it starts.
	q.EnqueueScan(false)
	if got := q.ScanStatus(); got.State != ScanRunning {
		t.Errorf("state = %q, want running (pending scan behind a run)", got.State)
	}

	q.markScanDone(ScanResult{
		Found:           10,
		Upserted:        9,
		Pruned:          1,
		AutoLinked:      3,
		Moved:           2,
		SkippedConflict: 1,
		Failed:          []string{"/music/bad.mp3"},
		KeptRoots:       []string{"/kept"},
		IncompleteRoots: []string{"/missing"},
	}, 1500*time.Millisecond)
	got := q.ScanStatus()
	if got.State != ScanDone {
		t.Fatalf("state = %q, want done", got.State)
	}
	if got.Found != 10 || got.Upserted != 9 || got.Pruned != 1 {
		t.Errorf("counters = %+v, want found=10 upserted=9 pruned=1", got)
	}
	if got.AutoLinked != 3 || got.Moved != 2 || got.SkippedConflict != 1 {
		t.Errorf("autolink = %+v, want 3/2/1", got)
	}
	if got.FailedCount != 1 || len(got.FailedFiles) != 1 {
		t.Errorf("failed = %d %v, want 1 path", got.FailedCount, got.FailedFiles)
	}
	if got.DurationMs != 1500 || got.FinishedAt == nil {
		t.Errorf("timing = %+v, want duration=1500 with finish time", got)
	}

	q.markScanFailed(errors.New("boom"), 500*time.Millisecond)
	got = q.ScanStatus()
	if got.State != ScanFailed {
		t.Fatalf("state = %q, want failed", got.State)
	}
	if got.Error != "boom" || got.FinishedAt == nil {
		t.Errorf("failed = %+v, want error boom with finish time", got)
	}
	if got.Found != 0 || got.FailedFiles != nil {
		t.Errorf("failed counters = %+v, want zeroed (no reliable counts)", got)
	}

	// Enqueueing after a terminal state starts a fresh lifecycle; the
	// zeroed failure carries no counters forward. (Pop the pending scan
	// queued behind the run first, as the runner would have run it.)
	if _, ok := q.pop(); !ok {
		t.Fatal("pop pending scan = false, want true")
	}
	q.EnqueueScan(false)
	got = q.ScanStatus()
	if got.State != ScanQueued {
		t.Fatalf("state = %q, want queued", got.State)
	}
	if got.StartedAt != nil || got.FinishedAt != nil || got.Error != "" {
		t.Errorf("stale lifecycle fields on queued: %+v", got)
	}
	if got.Found != 0 {
		t.Errorf("Found = %d, want 0 after failed run", got.Found)
	}
}

func TestScanEnqueuePreservesLastRunCounters(t *testing.T) {
	q := NewQueue(nil, nil)
	q.EnqueueScan(false)
	if _, ok := q.pop(); !ok {
		t.Fatal("pop scan task = false, want true")
	}
	q.markScanRunning()
	q.markScanDone(ScanResult{Found: 10, Upserted: 9}, time.Second)

	// A new enqueue keeps the last completed run's counters until the new
	// run finishes, with fresh lifecycle timestamps.
	q.EnqueueScan(false)
	got := q.ScanStatus()
	if got.State != ScanQueued {
		t.Fatalf("state = %q, want queued", got.State)
	}
	if got.Found != 10 || got.Upserted != 9 {
		t.Errorf("counters = %+v, want last run's 10/9 while queued", got)
	}
	if got.QueuedAt == nil || got.StartedAt != nil || got.FinishedAt != nil {
		t.Errorf("timestamps = %+v, want only queued_at", got)
	}
}

func TestScanFailedFilesBounded(t *testing.T) {
	q := NewQueue(nil, nil)
	q.EnqueueScan(false)
	q.markScanRunning()
	paths := make([]string, MaxScanFailedFiles+10)
	for i := range paths {
		paths[i] = "/music/f" + string(rune('0'+i%10)) + ".mp3"
	}
	q.markScanDone(ScanResult{Failed: paths}, time.Second)
	got := q.ScanStatus()
	if got.FailedCount != MaxScanFailedFiles+10 {
		t.Errorf("FailedCount = %d, want %d", got.FailedCount, MaxScanFailedFiles+10)
	}
	if len(got.FailedFiles) != MaxScanFailedFiles {
		t.Errorf("FailedFiles = %d, want bounded to %d", len(got.FailedFiles), MaxScanFailedFiles)
	}
}

func TestScanRootsEmptyMarksDone(t *testing.T) {
	r, _, q := newTestRunner(t)
	if got := q.ScanStatus(); got.State != ScanIdle {
		t.Fatalf("initial state = %q, want idle", got.State)
	}
	r.scanRoots(context.Background(), false)
	got := q.ScanStatus()
	if got.State != ScanDone {
		t.Fatalf("state = %q, want done after empty scan", got.State)
	}
	if got.FinishedAt == nil || got.StartedAt == nil || got.QueuedAt != nil {
		t.Errorf("timestamps = %+v, want started+finished, no queued (direct run)", got)
	}
}

func TestScanRootsFailureMarksFailed(t *testing.T) {
	log := utils.NewWithWriter(io.Discard)
	root := t.TempDir() // exists but empty: complete walk, zero files
	dbPath := filepath.Join(t.TempDir(), "test.db")
	st, err := store.NewStore(dbPath, []string{root}, log)
	if err != nil {
		t.Fatalf("NewStore = %v", err)
	}
	if err := st.AddRootDirs(context.Background()); err != nil {
		t.Fatalf("AddRootDirs = %v", err)
	}
	q := NewQueue(log, st)
	r := NewRunner(st, nil, log, q, nil, "")
	// Close the store so the sync fails; the in-memory status must still
	// record the failure even though persistence is best-effort.
	st.Close()
	r.scanRoots(context.Background(), false)
	got := q.ScanStatus()
	if got.State != ScanFailed {
		t.Fatalf("state = %q, want failed", got.State)
	}
	if got.Error == "" || got.FinishedAt == nil {
		t.Errorf("failed = %+v, want error with finish time", got)
	}
}

func TestScanFailedSurvivesRestart(t *testing.T) {
	log := utils.NewWithWriter(io.Discard)
	dbPath := filepath.Join(t.TempDir(), "test.db")
	st, err := store.NewStore(dbPath, nil, log)
	if err != nil {
		t.Fatalf("NewStore = %v", err)
	}
	q := NewQueue(log, st)
	q.EnqueueScan(false)
	q.markScanRunning()
	q.markScanFailed(errors.New("disk gone"), 250*time.Millisecond)

	st = reopenStore(t, dbPath, st)
	q2 := NewQueue(log, st)
	if err := q2.Recover(context.Background()); err != nil {
		t.Fatalf("Recover = %v", err)
	}
	got := q2.ScanStatus()
	if got.State != ScanFailed {
		t.Fatalf("recovered state = %q, want failed", got.State)
	}
	if got.Error != "disk gone" {
		t.Errorf("recovered error = %q, want 'disk gone'", got.Error)
	}
}

func TestScanDoneSurvivesRestart(t *testing.T) {
	log := utils.NewWithWriter(io.Discard)
	dbPath := filepath.Join(t.TempDir(), "test.db")
	st, err := store.NewStore(dbPath, nil, log)
	if err != nil {
		t.Fatalf("NewStore = %v", err)
	}
	q := NewQueue(log, st)
	r := NewRunner(st, nil, log, q, nil, "")
	r.scanRoots(context.Background(), false)
	if got := q.ScanStatus(); got.State != ScanDone {
		t.Fatalf("state = %q, want done", got.State)
	}

	st = reopenStore(t, dbPath, st)
	q2 := NewQueue(log, st)
	if err := q2.Recover(context.Background()); err != nil {
		t.Fatalf("Recover = %v", err)
	}
	got := q2.ScanStatus()
	if got.State != ScanDone {
		t.Fatalf("recovered state = %q, want done", got.State)
	}
	if got.FinishedAt == nil {
		t.Error("recovered FinishedAt nil")
	}
}
