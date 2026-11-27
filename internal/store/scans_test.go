package store

import (
	"testing"
	"time"

	apperrors "github.com/ppablomunoz/coda/internal/errors"
)

func TestScanRunRoundTrip(t *testing.T) {
	st := newTestStore(t)

	if _, err := st.GetScanRun(ctx()); !apperrors.Is(err, apperrors.ErrNotFound) {
		t.Fatalf("GetScanRun missing = %v, want NOT_FOUND", err)
	}

	now := time.Now().UTC().Truncate(time.Millisecond)
	queued := now.Add(-time.Minute)
	started := now.Add(-30 * time.Second)
	if err := st.UpsertScanRun(ctx(), ScanRun{
		State:           "done",
		QueuedAt:        &queued,
		StartedAt:       &started,
		FinishedAt:      &now,
		DurationMs:      30000,
		Found:           120,
		Upserted:        118,
		Pruned:          2,
		AutoLinked:      30,
		Moved:           12,
		SkippedConflict: 1,
		FailedCount:     2,
		FailedFiles:     []string{"/music/a.mp3", "/music/b.mp3"},
		KeptRoots:       []string{"/music"},
		IncompleteRoots: []string{"/missing"},
	}); err != nil {
		t.Fatalf("UpsertScanRun = %v", err)
	}
	got, err := st.GetScanRun(ctx())
	if err != nil {
		t.Fatalf("GetScanRun = %v", err)
	}
	if got.State != "done" || got.Found != 120 || got.Upserted != 118 || got.Pruned != 2 {
		t.Errorf("counters = %+v, want found=120 upserted=118 pruned=2", got)
	}
	if got.AutoLinked != 30 || got.Moved != 12 || got.SkippedConflict != 1 {
		t.Errorf("autolink = %+v, want linked=30 moved=12 skipped=1", got)
	}
	if got.FailedCount != 2 || len(got.FailedFiles) != 2 {
		t.Errorf("failed = %d %v, want count=2 files=2", got.FailedCount, got.FailedFiles)
	}
	if len(got.KeptRoots) != 1 || len(got.IncompleteRoots) != 1 {
		t.Errorf("roots = kept %v incomplete %v, want 1 each", got.KeptRoots, got.IncompleteRoots)
	}
	if got.DurationMs != 30000 || got.FinishedAt == nil || !got.FinishedAt.Equal(now) {
		t.Errorf("timing = %+v, want duration=30000 finished=%v", got, now)
	}

	// Overwrite with a failed run: the singleton row is replaced.
	finished := now.Add(time.Minute)
	if err := st.UpsertScanRun(ctx(), ScanRun{
		State: "failed", FinishedAt: &finished, Error: "boom",
	}); err != nil {
		t.Fatalf("UpsertScanRun failed = %v", err)
	}
	got, err = st.GetScanRun(ctx())
	if err != nil {
		t.Fatalf("GetScanRun failed = %v", err)
	}
	if got.State != "failed" || got.Error != "boom" {
		t.Errorf("failed row = %+v, want failed/boom", got)
	}
	if got.Found != 0 || got.FailedFiles != nil || got.KeptRoots != nil {
		t.Errorf("failed overwrite = %+v, want zeroed counters and nil lists", got)
	}
}
