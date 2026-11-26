package tasks

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/ppablomunoz/coda/internal/musicbrainz"
	"github.com/ppablomunoz/coda/internal/store"
	"github.com/ppablomunoz/coda/internal/utils"
)

func newTestRunner(t *testing.T) (*Runner, *store.Store, *Queue) {
	t.Helper()
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
	q := NewQueue(log)
	return NewRunner(st, mb, log, q, nil, ""), st, q
}

func TestExecUnknownKeyNoPanic(t *testing.T) {
	r, _, _ := newTestRunner(t)
	r.exec(context.Background(), Task{Key: "bogus"})
}

func TestExecScanRootsEmpty(t *testing.T) {
	r, _, _ := newTestRunner(t)
	r.exec(context.Background(), Task{Key: TaskScanRoots})
}

func TestImportArtistMissingID(t *testing.T) {
	r, _, q := newTestRunner(t)
	r.exec(context.Background(), Task{Key: TaskImportDiscography})
	if got := q.ArtistImportStatus(""); got.State != ArtistImportIdle {
		t.Errorf("empty id status = %q, want idle (skipped)", got.State)
	}
}

func TestImportArtistNotFoundForgetsStatus(t *testing.T) {
	r, _, q := newTestRunner(t)
	id := "00000000-0000-4000-8000-000000000001"
	q.EnqueueRefreshArtist(id)
	r.exec(context.Background(), Task{Key: TaskImportDiscography, ID: id})
	if got := q.ArtistImportStatus(id); got.State != ArtistImportIdle {
		t.Errorf("state = %q, want idle for removed artist (status forgotten, not stuck done)", got.State)
	}
}

func TestImportArtistNonLibrarySkipped(t *testing.T) {
	r, st, q := newTestRunner(t)
	mainID := "d5b32d03-2aaf-4baf-ba5d-3721d095b833"
	guestID := "a1b2c3d4-e5f6-47ab-8cd0-ef1234567890"
	if err := st.CreateArtist(context.Background(), store.Artist{ID: mainID, Name: "Main"}); err != nil {
		t.Fatalf("CreateArtist = %v", err)
	}
	rel := store.FullReleaseParams{
		ID:    "33333333-4444-4555-b666-777777777777",
		Title: "Rel",
		Credit: []store.CreditEntry{
			{ArtistID: mainID, Name: "Main", CreditedName: "Main"},
			{ArtistID: guestID, Name: "Guest", CreditedName: "Guest"},
		},
		Group: store.ReleaseGroupParams{
			ID:    "11111111-2222-4333-8444-555555555555",
			Title: "Group", Type: "album",
			Credit: []store.CreditEntry{
				{ArtistID: mainID, Name: "Main", CreditedName: "Main"},
				{ArtistID: guestID, Name: "Guest", CreditedName: "Guest"},
			},
		},
		Tracks: []store.TrackParams{{
			ID: "55555555-6666-4777-b888-999999999999", ReleaseID: "33333333-4444-4555-b666-777777777777",
			Disc: 1, Position: 1, Number: "1", Title: "Song", RecordingID: "77777777-8888-4999-baaa-bbbbbbbbbbbb",
			Credit: []store.CreditEntry{{ArtistID: guestID, Name: "Guest", CreditedName: "Guest"}},
		}},
	}
	if err := st.SaveDiscography(context.Background(), []store.FullReleaseParams{rel}); err != nil {
		t.Fatalf("SaveDiscography = %v", err)
	}
	q.EnqueueRefreshArtist(guestID)
	r.exec(context.Background(), Task{Key: TaskImportDiscography, ID: guestID})
	if got := q.ArtistImportStatus(guestID); got.State != ArtistImportDone {
		t.Errorf("state = %q, want done for non-library", got.State)
	}
}

func TestExecPrune(t *testing.T) {
	r, _, _ := newTestRunner(t)
	r.exec(context.Background(), Task{Key: TaskPruneArtists})
}

func TestScanRootsGuardsPrune(t *testing.T) {
	log := utils.NewWithWriter(io.Discard)
	rootGood := t.TempDir() // exists but empty: complete walk, zero files
	rootMissing := filepath.Join(t.TempDir(), "gone")

	st, err := store.NewStore(filepath.Join(t.TempDir(), "test.db"), []string{rootGood, rootMissing}, log)
	if err != nil {
		t.Fatalf("NewStore = %v", err)
	}
	t.Cleanup(st.Close)
	if err := st.AddRootDirs(context.Background()); err != nil {
		t.Fatalf("AddRootDirs = %v", err)
	}
	srv := httptest.NewServer(http.NewServeMux())
	t.Cleanup(srv.Close)
	mb, err := musicbrainz.New(musicbrainz.Config{UserAgent: "coda-test", BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("mb New = %v", err)
	}
	q := NewQueue(log)
	r := NewRunner(st, mb, log, q, nil, "")

	// Seed one row per root. The files don't exist on disk, so the tag
	// read fails but the rows still sync (existing links preserved).
	if _, err := st.SyncLocalFiles(context.Background(), []store.ScannedFile{
		{LibraryRoot: rootGood, Path: filepath.Join(rootGood, "old.mp3"), SizeBytes: 100, MtimeNs: 123, FormatHint: "mp3"},
		{LibraryRoot: rootMissing, Path: filepath.Join(rootMissing, "old.mp3"), SizeBytes: 100, MtimeNs: 123, FormatHint: "mp3"},
	}, "2024-01-01 00:00:00", store.SyncOptions{ForcePrune: true}); err != nil {
		t.Fatalf("seed SyncLocalFiles = %v", err)
	}

	// Normal scan: the empty root keeps its rows (unexpected empty) and
	// the missing root keeps its rows (incomplete walk).
	r.scanRoots(context.Background(), false)
	files, total, err := st.ListFiles(context.Background(), store.FileListOptions{PageOptions: store.PageOptions{Limit: 20}})
	if err != nil {
		t.Fatalf("ListFiles = %v", err)
	}
	if total != 2 || len(files) != 2 {
		t.Fatalf("after guarded scan total = %d, want 2 (both roots kept)", total)
	}

	// Forced scan: the empty-but-readable root prunes, the missing root
	// still keeps its rows.
	r.scanRoots(context.Background(), true)
	files, total, err = st.ListFiles(context.Background(), store.FileListOptions{PageOptions: store.PageOptions{Limit: 20}})
	if err != nil {
		t.Fatalf("ListFiles = %v", err)
	}
	if total != 1 || len(files) != 1 {
		t.Fatalf("after force scan total = %d files=%v, want 1 (missing root only)", total, files)
	}
	if got := files[0].Path; got != filepath.Join(rootMissing, "old.mp3") {
		t.Errorf("remaining path = %q, want missing-root row", got)
	}
}
