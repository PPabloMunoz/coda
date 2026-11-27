package tasks

import (
	"context"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/ppablomunoz/coda/internal/store"
	"github.com/ppablomunoz/coda/internal/utils"
)

func logForTest() *utils.Logger { return utils.NewWithWriter(io.Discard) }

func newStoreAtPath(t *testing.T, dbPath string) *store.Store {
	t.Helper()
	st, err := store.NewStore(dbPath, nil, logForTest())
	if err != nil {
		t.Fatalf("NewStore = %v", err)
	}
	t.Cleanup(st.Close)
	return st
}

// TestScanTimingsRoundTrip is the #46 acceptance: per-phase timings and
// incremental counters survive the queue -> store -> recover round trip and
// show up in the status.
func TestScanTimingsRoundTrip(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	st := newStoreAtPath(t, dbPath)
	q := NewQueue(logForTest(), st)
	q.EnqueueScan(false)
	if _, ok := q.pop(); !ok {
		t.Fatal("pop scan = false, want true")
	}
	q.markScanRunning()
	q.markScanDone(ScanResult{
		Found: 10, Upserted: 9, Pruned: 1, AutoLinked: 3, Moved: 2,
		WalkMs: 120, SyncMs: 340, TagReadMs: 200, AutolinkMs: 45,
		TagReads: 4, Unchanged: 6,
	}, 1500*time.Millisecond)

	got := q.ScanStatus()
	if got.WalkMs != 120 || got.SyncMs != 340 || got.TagReadMs != 200 || got.AutolinkMs != 45 {
		t.Fatalf("phase ms = %d/%d/%d/%d, want 120/340/200/45",
			got.WalkMs, got.SyncMs, got.TagReadMs, got.AutolinkMs)
	}
	if got.TagReads != 4 || got.Unchanged != 6 {
		t.Fatalf("counts tag_reads=%d unchanged=%d, want 4/6", got.TagReads, got.Unchanged)
	}

	st = reopenStore(t, dbPath, st)
	q2 := NewQueue(logForTest(), st)
	if err := q2.Recover(context.Background()); err != nil {
		t.Fatalf("Recover = %v", err)
	}
	got2 := q2.ScanStatus()
	if got2.State != ScanDone {
		t.Fatalf("recovered state = %q, want done", got2.State)
	}
	if got2.WalkMs != 120 || got2.SyncMs != 340 || got2.TagReadMs != 200 || got2.AutolinkMs != 45 {
		t.Errorf("recovered phase ms = %d/%d/%d/%d, want 120/340/200/45",
			got2.WalkMs, got2.SyncMs, got2.TagReadMs, got2.AutolinkMs)
	}
	if got2.TagReads != 4 || got2.Unchanged != 6 {
		t.Errorf("recovered counts tag_reads=%d unchanged=%d, want 4/6", got2.TagReads, got2.Unchanged)
	}
}
