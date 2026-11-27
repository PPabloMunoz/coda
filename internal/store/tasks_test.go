package store

import (
	"testing"
	"time"

	apperrors "github.com/ppablomunoz/coda/internal/errors"
)

func seedTwoReleases(t *testing.T, st *Store) (string, string) {
	t.Helper()
	relA := "33333333-4444-4555-b666-777777777777"
	relB := "44444444-5555-4666-b777-888888888888"
	groupA := "11111111-2222-4333-8444-555555555555"
	groupB := "22222222-3333-4444-a555-666666666666"
	if err := st.CreateArtist(ctx(), Artist{ID: testArtistID, Name: "Test Artist"}); err != nil {
		t.Fatalf("CreateArtist = %v", err)
	}
	if err := st.SaveDiscography(ctx(), []FullReleaseParams{
		testRelease(testTrackID, relA, groupA),
		{
			ID:     relB,
			Title:  "Second Release",
			Credit: testCredit(testArtistID, "Test Artist"),
			Group: ReleaseGroupParams{
				ID: groupB, Title: "Second Group", Type: "album",
				Credit: testCredit(testArtistID, "Test Artist"),
			},
			Tracks: []TrackParams{{
				ID: testTrackID2, ReleaseID: relB,
				Disc: 1, Position: 1, Number: "1", Title: "Second Track",
				Credit:      testCredit(testArtistID, "Test Artist"),
				RecordingID: testRecID,
			}},
		},
	}); err != nil {
		t.Fatalf("SaveDiscography = %v", err)
	}
	return relA, relB
}

func TestArtistImportRoundTrip(t *testing.T) {
	st := newTestStore(t)
	now := time.Now().UTC().Truncate(time.Millisecond)

	if _, err := st.GetArtistImport(ctx(), testArtistID); !apperrors.Is(err, apperrors.ErrNotFound) {
		t.Fatalf("GetArtistImport missing = %v, want NOT_FOUND", err)
	}

	if err := st.UpsertArtistImport(ctx(), ArtistImport{
		ArtistID: testArtistID, State: "queued", QueuedAt: &now,
	}); err != nil {
		t.Fatalf("UpsertArtistImport = %v", err)
	}
	got, err := st.GetArtistImport(ctx(), testArtistID)
	if err != nil {
		t.Fatalf("GetArtistImport = %v", err)
	}
	if got.State != "queued" || got.Error != "" {
		t.Errorf("got state=%q error=%q, want queued/empty", got.State, got.Error)
	}
	if got.QueuedAt == nil || !got.QueuedAt.Equal(now) {
		t.Errorf("queued_at = %v, want %v", got.QueuedAt, now)
	}
	if got.StartedAt != nil || got.FinishedAt != nil {
		t.Errorf("started/finished set on queued row: %+v", got)
	}

	started := now.Add(time.Minute)
	if err := st.UpsertArtistImport(ctx(), ArtistImport{
		ArtistID: testArtistID, State: "running", QueuedAt: &now, StartedAt: &started,
	}); err != nil {
		t.Fatalf("UpsertArtistImport running = %v", err)
	}
	got, err = st.GetArtistImport(ctx(), testArtistID)
	if err != nil {
		t.Fatalf("GetArtistImport running = %v", err)
	}
	if got.State != "running" || got.StartedAt == nil || !got.StartedAt.Equal(started) {
		t.Errorf("running row = %+v, want started=%v", got, started)
	}

	finished := now.Add(2 * time.Minute)
	if err := st.UpsertArtistImport(ctx(), ArtistImport{
		ArtistID: testArtistID, State: "failed",
		QueuedAt: &now, StartedAt: &started, FinishedAt: &finished, Error: "boom",
	}); err != nil {
		t.Fatalf("UpsertArtistImport failed = %v", err)
	}
	got, err = st.GetArtistImport(ctx(), testArtistID)
	if err != nil {
		t.Fatalf("GetArtistImport failed = %v", err)
	}
	if got.State != "failed" || got.Error != "boom" || got.FinishedAt == nil {
		t.Errorf("failed row = %+v", got)
	}

	if err := st.DeleteArtistImport(ctx(), testArtistID); err != nil {
		t.Fatalf("DeleteArtistImport = %v", err)
	}
	// Idempotent: deleting again is a no-op.
	if err := st.DeleteArtistImport(ctx(), testArtistID); err != nil {
		t.Fatalf("DeleteArtistImport idempotent = %v", err)
	}
	if _, err := st.GetArtistImport(ctx(), testArtistID); !apperrors.Is(err, apperrors.ErrNotFound) {
		t.Errorf("GetArtistImport after delete = %v, want NOT_FOUND", err)
	}
}

func TestPendingReleaseTasksFIFOAndCoalesce(t *testing.T) {
	st := newTestStore(t)
	relA, relB := seedTwoReleases(t, st)

	if tasks, err := st.ListPendingReleaseTasks(ctx()); err != nil {
		t.Fatalf("ListPendingReleaseTasks empty = %v", err)
	} else if len(tasks) != 0 {
		t.Fatalf("pending = %d, want 0", len(tasks))
	}

	if err := st.InsertPendingReleaseTask(ctx(), relA, PendingTaskEmbed); err != nil {
		t.Fatalf("insert embed A = %v", err)
	}
	if err := st.InsertPendingReleaseTask(ctx(), relA, PendingTaskClear); err != nil {
		t.Fatalf("insert clear A = %v", err)
	}
	if err := st.InsertPendingReleaseTask(ctx(), relB, PendingTaskRelink); err != nil {
		t.Fatalf("insert relink B = %v", err)
	}
	// Repeating an enqueue coalesces: no new row, no error.
	if err := st.InsertPendingReleaseTask(ctx(), relA, PendingTaskEmbed); err != nil {
		t.Fatalf("re-insert embed A = %v", err)
	}

	tasks, err := st.ListPendingReleaseTasks(ctx())
	if err != nil {
		t.Fatalf("ListPendingReleaseTasks = %v", err)
	}
	if len(tasks) != 3 {
		t.Fatalf("pending = %d, want 3 (coalesced)", len(tasks))
	}
	want := []struct {
		release, kind string
	}{
		{relA, PendingTaskEmbed},
		{relA, PendingTaskClear},
		{relB, PendingTaskRelink},
	}
	for i, w := range want {
		if tasks[i].ReleaseID != w.release || tasks[i].Kind != w.kind {
			t.Errorf("task[%d] = %s/%s, want %s/%s", i,
				tasks[i].ReleaseID, tasks[i].Kind, w.release, w.kind)
		}
	}
	if tasks[0].ID >= tasks[1].ID || tasks[1].ID >= tasks[2].ID {
		t.Errorf("ids not FIFO-ordered: %d %d %d", tasks[0].ID, tasks[1].ID, tasks[2].ID)
	}

	if err := st.DeletePendingReleaseTask(ctx(), relA, PendingTaskEmbed); err != nil {
		t.Fatalf("delete embed A = %v", err)
	}
	// Idempotent: deleting again is a no-op.
	if err := st.DeletePendingReleaseTask(ctx(), relA, PendingTaskEmbed); err != nil {
		t.Fatalf("delete embed A idempotent = %v", err)
	}
	tasks, err = st.ListPendingReleaseTasks(ctx())
	if err != nil {
		t.Fatalf("ListPendingReleaseTasks after delete = %v", err)
	}
	if len(tasks) != 2 || tasks[0].Kind != PendingTaskClear {
		t.Errorf("after delete = %+v, want clear A first", tasks)
	}
}
