package tasks

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/ppablomunoz/coda/internal/artwork"
	"github.com/ppablomunoz/coda/internal/musicbrainz"
	"github.com/ppablomunoz/coda/internal/store"
	"github.com/ppablomunoz/coda/internal/utils"
)

// Recovery tests simulate a restart: work is enqueued against one Store,
// the Store is closed, a new Store opens the same DB file, and Recover
// rehydrates a fresh Queue.
const (
	recoverArtistID = "d5b32d03-2aaf-4baf-ba5d-3721d095b833"
	recoverGroupID  = "11111111-2222-4333-8444-555555555555"
	recoverGroupB   = "22222222-3333-4444-a555-666666666666"
	recoverRelID    = "33333333-4444-4555-b666-777777777777"
	recoverRelB     = "44444444-5555-4666-b777-888888888888"
	recoverTrackID  = "55555555-6666-4777-b888-999999999999"
	recoverTrackB   = "66666666-7777-4888-b999-aaaaaaaaaaaa"
	recoverRecID    = "77777777-8888-4999-baaa-bbbbbbbbbbbb"
)

func reopenStore(t *testing.T, dbPath string, st *store.Store) *store.Store {
	t.Helper()
	st.Close()
	log := utils.NewWithWriter(io.Discard)
	reopened, err := store.NewStore(dbPath, nil, log)
	if err != nil {
		t.Fatalf("reopen NewStore = %v", err)
	}
	t.Cleanup(reopened.Close)
	return reopened
}

func seedRecoverReleases(t *testing.T, st *store.Store) {
	t.Helper()
	if err := st.CreateArtist(context.Background(),
		store.Artist{ID: recoverArtistID, Name: "Recover Artist"}); err != nil {
		t.Fatalf("CreateArtist = %v", err)
	}
	credit := []store.CreditEntry{{ArtistID: recoverArtistID, Name: "Recover Artist", CreditedName: "Recover Artist"}}
	if err := st.SaveDiscography(context.Background(), []store.FullReleaseParams{
		{
			ID: recoverRelID, Title: "Recover Release", Credit: credit,
			Group: store.ReleaseGroupParams{
				ID: recoverGroupID, Title: "Recover Group", Type: "album", Credit: credit,
			},
			Tracks: []store.TrackParams{{
				ID: recoverTrackID, ReleaseID: recoverRelID,
				Disc: 1, Position: 1, Number: "1", Title: "Recover Track",
				Credit: credit, RecordingID: recoverRecID,
			}},
		},
		{
			ID: recoverRelB, Title: "Second Release", Credit: credit,
			Group: store.ReleaseGroupParams{
				ID: recoverGroupB, Title: "Second Group", Type: "album", Credit: credit,
			},
			Tracks: []store.TrackParams{{
				ID: recoverTrackB, ReleaseID: recoverRelB,
				Disc: 1, Position: 1, Number: "1", Title: "Second Track",
				Credit: credit, RecordingID: recoverRecID,
			}},
		},
	}); err != nil {
		t.Fatalf("SaveDiscography = %v", err)
	}
}

func TestRecoverReenqueuesInterruptedImport(t *testing.T) {
	log := utils.NewWithWriter(io.Discard)
	dbPath := filepath.Join(t.TempDir(), "test.db")
	st, err := store.NewStore(dbPath, nil, log)
	if err != nil {
		t.Fatalf("NewStore = %v", err)
	}
	if err := st.CreateArtist(context.Background(),
		store.Artist{ID: recoverArtistID, Name: "Recover Artist"}); err != nil {
		t.Fatalf("CreateArtist = %v", err)
	}

	q1 := NewQueue(log, st)
	q1.EnqueueRefreshArtist(recoverArtistID)
	q1.markArtistImportRunning(recoverArtistID)

	st = reopenStore(t, dbPath, st)
	q2 := NewQueue(log, st)
	if err := q2.Recover(context.Background()); err != nil {
		t.Fatalf("Recover = %v", err)
	}

	got := q2.ArtistImportStatus(recoverArtistID)
	if got.State != ArtistImportQueued {
		t.Errorf("state = %q, want queued (interrupted run re-enqueued)", got.State)
	}
	if got.QueuedAt == nil {
		t.Error("QueuedAt nil after recover, want original queue time preserved")
	}
	if got.StartedAt != nil || got.FinishedAt != nil || got.Error != "" {
		t.Errorf("stale timestamps/error after recover: %+v", got)
	}
	if !q2.HasTask(TaskImportDiscography, recoverArtistID) {
		t.Error("import task not re-enqueued after recover")
	}
}

func TestRecoverPreservesTerminalImportState(t *testing.T) {
	log := utils.NewWithWriter(io.Discard)
	dbPath := filepath.Join(t.TempDir(), "test.db")
	st, err := store.NewStore(dbPath, nil, log)
	if err != nil {
		t.Fatalf("NewStore = %v", err)
	}
	if err := st.CreateArtist(context.Background(),
		store.Artist{ID: recoverArtistID, Name: "Recover Artist"}); err != nil {
		t.Fatalf("CreateArtist = %v", err)
	}

	q1 := NewQueue(log, st)
	q1.EnqueueRefreshArtist(recoverArtistID)
	q1.markArtistImportRunning(recoverArtistID)
	q1.markArtistImportDone(recoverArtistID)

	st = reopenStore(t, dbPath, st)
	q2 := NewQueue(log, st)
	if err := q2.Recover(context.Background()); err != nil {
		t.Fatalf("Recover = %v", err)
	}

	got := q2.ArtistImportStatus(recoverArtistID)
	if got.State != ArtistImportDone {
		t.Errorf("state = %q, want done (terminal state survives restart)", got.State)
	}
	if got.FinishedAt == nil {
		t.Error("FinishedAt nil after recover, want preserved")
	}
	if q2.HasTask(TaskImportDiscography, recoverArtistID) {
		t.Error("done import re-enqueued, want no task")
	}

	// A failed import hydrates the same way, with its error intact.
	q2.EnqueueRefreshArtist(recoverArtistID)
	q2.markArtistImportRunning(recoverArtistID)
	q2.markArtistImportFailed(recoverArtistID, errors.New("boom"))
	st = reopenStore(t, dbPath, st)
	q3 := NewQueue(log, st)
	if err := q3.Recover(context.Background()); err != nil {
		t.Fatalf("Recover = %v", err)
	}
	failed := q3.ArtistImportStatus(recoverArtistID)
	if failed.State != ArtistImportFailed || failed.Error != "boom" {
		t.Errorf("failed = %+v, want failed/boom", failed)
	}
	if q3.HasTask(TaskImportDiscography, recoverArtistID) {
		t.Error("failed import re-enqueued, want no task")
	}
}

func TestRecoverReenqueuesPendingFileTasksFIFO(t *testing.T) {
	log := utils.NewWithWriter(io.Discard)
	dbPath := filepath.Join(t.TempDir(), "test.db")
	st, err := store.NewStore(dbPath, nil, log)
	if err != nil {
		t.Fatalf("NewStore = %v", err)
	}
	seedRecoverReleases(t, st)

	q1 := NewQueue(log, st)
	q1.EnqueueEmbedReleaseCover(recoverRelID)
	q1.EnqueueClearReleaseCover(recoverRelID)
	q1.EnqueueRelinkRelease(recoverRelB)

	st = reopenStore(t, dbPath, st)
	q2 := NewQueue(log, st)
	if err := q2.Recover(context.Background()); err != nil {
		t.Fatalf("Recover = %v", err)
	}

	// FIFO order across kinds is preserved.
	want := []Task{
		{Key: TaskEmbedReleaseCover, ID: recoverRelID},
		{Key: TaskClearReleaseCover, ID: recoverRelID},
		{Key: TaskRelinkRelease, ID: recoverRelB},
	}
	for i, w := range want {
		got, ok := q2.pop()
		if !ok {
			t.Fatalf("pop %d: queue empty, want %s %q", i, w.Key, w.ID)
		}
		if got.Key != w.Key || got.ID != w.ID {
			t.Errorf("task[%d] = %s %q, want %s %q", i, got.Key, got.ID, w.Key, w.ID)
		}
	}
}

func TestShutdownKeepsPendingRowForRecovery(t *testing.T) {
	log := utils.NewWithWriter(io.Discard)
	dbPath := filepath.Join(t.TempDir(), "test.db")
	st, err := store.NewStore(dbPath, nil, log)
	if err != nil {
		t.Fatalf("NewStore = %v", err)
	}
	seedRecoverReleases(t, st)

	q := NewQueue(log, st)
	q.EnqueueEmbedReleaseCover(recoverRelID)

	// A canceled run context is a shutdown: the task is consumed but its
	// durable row is kept so the next startup recovers it.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	q.run(ctx, func(context.Context, Task) {})

	pending, err := st.ListPendingReleaseTasks(context.Background())
	if err != nil {
		t.Fatalf("ListPendingReleaseTasks = %v", err)
	}
	if len(pending) != 1 || pending[0].ReleaseID != recoverRelID {
		t.Fatalf("pending after shutdown = %+v, want the embed row kept", pending)
	}

	q2 := NewQueue(log, st)
	if err := q2.Recover(context.Background()); err != nil {
		t.Fatalf("Recover = %v", err)
	}
	if !q2.HasTask(TaskEmbedReleaseCover, recoverRelID) {
		t.Error("embed task not re-enqueued after shutdown recovery")
	}
}

func TestCanceledImportStaysRunning(t *testing.T) {
	log := utils.NewWithWriter(io.Discard)
	st, err := store.NewStore(filepath.Join(t.TempDir(), "test.db"), nil, log)
	if err != nil {
		t.Fatalf("NewStore = %v", err)
	}
	t.Cleanup(st.Close)
	if err := st.CreateArtist(context.Background(),
		store.Artist{ID: recoverArtistID, Name: "Recover Artist"}); err != nil {
		t.Fatalf("CreateArtist = %v", err)
	}
	srv := httptest.NewServer(http.NewServeMux())
	t.Cleanup(srv.Close)
	mb, err := musicbrainz.New(musicbrainz.Config{UserAgent: "coda-test", BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("mb New = %v", err)
	}
	q := NewQueue(log, st)
	r := NewRunner(st, mb, log, q, nil, "")

	// Shutdown mid-import: the canceled parent surfaces as a DB error, but
	// the import must stay running (recovered on restart), not failed.
	shut, stop := context.WithCancel(context.Background())
	r.baseCtx = shut
	stop()
	q.EnqueueRefreshArtist(recoverArtistID)
	r.exec(shut, Task{Key: TaskImportDiscography, ID: recoverArtistID})

	if got := q.ArtistImportStatus(recoverArtistID); got.State != ArtistImportRunning {
		t.Errorf("state = %q, want running (shutdown interrupts, never fails)", got.State)
	}
	if imp, err := st.GetArtistImport(context.Background(), recoverArtistID); err != nil {
		t.Fatalf("GetArtistImport = %v", err)
	} else if imp.State != string(ArtistImportRunning) {
		t.Errorf("persisted state = %q, want running", imp.State)
	}
}

func TestImportFailureMarksFailedWhenRunning(t *testing.T) {
	log := utils.NewWithWriter(io.Discard)
	st, err := store.NewStore(filepath.Join(t.TempDir(), "test.db"), nil, log)
	if err != nil {
		t.Fatalf("NewStore = %v", err)
	}
	t.Cleanup(st.Close)
	if err := st.CreateArtist(context.Background(),
		store.Artist{ID: recoverArtistID, Name: "Recover Artist"}); err != nil {
		t.Fatalf("CreateArtist = %v", err)
	}
	// 400 fails fast (no retry backoff): a live failure must still record.
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	mb, err := musicbrainz.New(musicbrainz.Config{UserAgent: "coda-test", BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("mb New = %v", err)
	}
	q := NewQueue(log, st)
	r := NewRunner(st, mb, log, q, nil, "")
	r.baseCtx = context.Background()

	q.EnqueueRefreshArtist(recoverArtistID)
	r.exec(context.Background(), Task{Key: TaskImportDiscography, ID: recoverArtistID})

	got := q.ArtistImportStatus(recoverArtistID)
	if got.State != ArtistImportFailed {
		t.Errorf("state = %q, want failed (live failure still records)", got.State)
	}
	if got.Error == "" || got.FinishedAt == nil {
		t.Errorf("failed status missing error/finish: %+v", got)
	}
}

func TestRunnerStartWaitsForDetached(t *testing.T) {
	log := utils.NewWithWriter(io.Discard)
	st, err := store.NewStore(filepath.Join(t.TempDir(), "test.db"), nil, log)
	if err != nil {
		t.Fatalf("NewStore = %v", err)
	}
	t.Cleanup(st.Close)
	srv := httptest.NewServer(http.NewServeMux())
	t.Cleanup(srv.Close)
	mb, err := musicbrainz.New(musicbrainz.Config{UserAgent: "coda-test", BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("mb New = %v", err)
	}
	q := NewQueue(log, st)
	r := NewRunner(st, mb, log, q, nil, "")

	ctx, cancel := context.WithCancel(context.Background())
	release := make(chan struct{})
	r.bgWg.Add(1)
	go func() {
		defer r.bgWg.Done()
		<-ctx.Done()
		close(release)
	}()
	done := make(chan struct{})
	go func() {
		r.Start(ctx)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not return after shutdown (detached work not tracked)")
	}
	select {
	case <-release:
	default:
		t.Error("detached work still running after Start returned")
	}
}

func TestSpawnPrefetchCancelsOnShutdown(t *testing.T) {
	log := utils.NewWithWriter(io.Discard)
	st, err := store.NewStore(filepath.Join(t.TempDir(), "test.db"), nil, log)
	if err != nil {
		t.Fatalf("NewStore = %v", err)
	}
	t.Cleanup(st.Close)
	srv := httptest.NewServer(http.NewServeMux())
	t.Cleanup(srv.Close)
	mb, err := musicbrainz.New(musicbrainz.Config{UserAgent: "coda-test", BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("mb New = %v", err)
	}
	art, err := artwork.New(artwork.Config{UserAgent: "coda-test"})
	if err != nil {
		t.Fatalf("artwork New = %v", err)
	}
	q := NewQueue(log, st)
	r := NewRunner(st, mb, log, q, art, "")

	ctx, cancel := context.WithCancel(context.Background())
	r.baseCtx = ctx
	finished := make(chan struct{})
	r.spawnPrefetch(func(bctx context.Context) {
		<-bctx.Done()
		close(finished)
	}, 30*time.Minute)
	cancel()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("detached run did not cancel on shutdown")
	}
	r.bgWg.Wait()
}

func TestRecoverNilStoreNoPanic(t *testing.T) {
	q := NewQueue(nil, nil)
	if err := q.Recover(context.Background()); err != nil {
		t.Errorf("Recover nil store = %v, want nil", err)
	}
}
