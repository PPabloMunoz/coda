package store

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	apperrors "github.com/ppablomunoz/coda/internal/errors"
)

func insertFile(t *testing.T, st *Store, rootID int64, path string, trackID *string) int64 {
	t.Helper()
	format := strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))
	res, err := st.db.Exec(
		`INSERT INTO local_files (root_id, path, size_bytes, mtime_ns, format_hint, scanned_at, track_id)
		 VALUES (?, ?, 100, 123, ?, '2024-01-01 00:00:00', ?)`,
		rootID, path, format, trackID,
	)
	if err != nil {
		t.Fatalf("insert file = %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

func testRootID(t *testing.T, st *Store) int64 {
	t.Helper()
	var id int64
	if err := st.db.QueryRow(`SELECT id FROM roots LIMIT 1`).Scan(&id); err != nil {
		t.Fatalf("no root: %v", err)
	}
	return id
}

func TestSyncLocalFilesValidation(t *testing.T) {
	st := newTestStore(t) // no roots
	stats, err := st.SyncLocalFiles(ctx(), nil, "2024-01-01 00:00:00", SyncOptions{})
	if err != nil || stats.Found != 0 {
		t.Errorf("no-roots sync = %+v, %v; want empty", stats, err)
	}

	root := t.TempDir()
	st2 := newTestStore(t, root)
	if _, err := st2.SyncLocalFiles(ctx(), nil, "  ", SyncOptions{}); err == nil {
		t.Error("blank scanStart = nil, want error")
	}

	if _, err := st2.SyncLocalFiles(ctx(), []ScannedFile{{
		LibraryRoot: "/unknown", Path: "/unknown/a.mp3",
		SizeBytes: 1, MtimeNs: 1, FormatHint: "mp3",
	}}, "2024-01-01 00:00:00", SyncOptions{}); !apperrors.Is(err, apperrors.ErrNotFound) {
		t.Errorf("unknown root err = %v, want NOT_FOUND", err)
	}
}

func TestSyncLocalFilesPrunesStale(t *testing.T) {
	root := t.TempDir()
	st := newTestStore(t, root)
	rootID := testRootID(t, st)
	insertFile(t, st, rootID, root+"/old.mp3", nil)
	keep := root + "/keep.mp3"
	insertFile(t, st, rootID, keep, nil)

	// Partial deletion still prunes: one file vanished, one survived.
	stats, err := st.SyncLocalFiles(ctx(), []ScannedFile{{
		LibraryRoot: root, Path: keep,
		SizeBytes: 100, MtimeNs: 123, FormatHint: "mp3",
	}}, "2024-06-01 00:00:00", SyncOptions{})
	if err != nil {
		t.Fatalf("SyncLocalFiles = %v", err)
	}
	if stats.Pruned != 1 {
		t.Errorf("pruned = %d, want 1", stats.Pruned)
	}
	files, total, err := st.ListFiles(ctx(), FileListOptions{PageOptions: PageOptions{Limit: 20}})
	if err != nil {
		t.Fatalf("ListFiles = %v", err)
	}
	if total != 1 || len(files) != 1 || files[0].Path != keep {
		t.Errorf("after prune total = %d files=%v, want 1 kept %q", total, files, keep)
	}
}

func TestSyncLocalFilesKeepsUnexpectedEmptyRoot(t *testing.T) {
	root := t.TempDir()
	st := newTestStore(t, root)
	rootID := testRootID(t, st)
	insertFile(t, st, rootID, root+"/old.mp3", nil)

	// A complete walk that suddenly yields zero files keeps rows by default.
	stats, err := st.SyncLocalFiles(ctx(), nil, "2024-06-01 00:00:00", SyncOptions{})
	if err != nil {
		t.Fatalf("SyncLocalFiles = %v", err)
	}
	if stats.Pruned != 0 {
		t.Errorf("pruned = %d, want 0 (empty root must not prune)", stats.Pruned)
	}
	if len(stats.KeptRoots) != 1 || stats.KeptRoots[0] != root {
		t.Errorf("KeptRoots = %v, want [%q]", stats.KeptRoots, root)
	}
	_, total, err := st.ListFiles(ctx(), FileListOptions{PageOptions: PageOptions{Limit: 20}})
	if err != nil {
		t.Fatalf("ListFiles = %v", err)
	}
	if total != 1 {
		t.Errorf("after empty sync total = %d, want 1 (rows kept)", total)
	}
}

func TestSyncLocalFilesForcePrunesEmptyRoot(t *testing.T) {
	root := t.TempDir()
	st := newTestStore(t, root)
	rootID := testRootID(t, st)
	insertFile(t, st, rootID, root+"/old.mp3", nil)

	stats, err := st.SyncLocalFiles(ctx(), nil, "2024-06-01 00:00:00", SyncOptions{ForcePrune: true})
	if err != nil {
		t.Fatalf("SyncLocalFiles = %v", err)
	}
	if stats.Pruned != 1 {
		t.Errorf("pruned = %d, want 1 (force prune clears empty root)", stats.Pruned)
	}
	if len(stats.KeptRoots) != 0 {
		t.Errorf("KeptRoots = %v, want empty on force prune", stats.KeptRoots)
	}
	_, total, err := st.ListFiles(ctx(), FileListOptions{PageOptions: PageOptions{Limit: 20}})
	if err != nil {
		t.Fatalf("ListFiles = %v", err)
	}
	if total != 0 {
		t.Errorf("after force prune total = %d, want 0", total)
	}
}

func TestSyncLocalFilesSkipsIncompleteRoot(t *testing.T) {
	root := t.TempDir()
	st := newTestStore(t, root)
	rootID := testRootID(t, st)
	insertFile(t, st, rootID, root+"/old.mp3", nil)

	for _, opts := range []SyncOptions{
		{IncompleteRoots: []string{root}},
		{IncompleteRoots: []string{root}, ForcePrune: true},
	} {
		stats, err := st.SyncLocalFiles(ctx(), nil, "2024-06-01 00:00:00", opts)
		if err != nil {
			t.Fatalf("SyncLocalFiles(%+v) = %v", opts, err)
		}
		if stats.Pruned != 0 {
			t.Errorf("SyncLocalFiles(%+v) pruned = %d, want 0 (incomplete never prunes)", opts, stats.Pruned)
		}
		if len(stats.KeptRoots) != 1 || stats.KeptRoots[0] != root {
			t.Errorf("SyncLocalFiles(%+v) KeptRoots = %v, want [%q]", opts, stats.KeptRoots, root)
		}
	}
	_, total, err := st.ListFiles(ctx(), FileListOptions{PageOptions: PageOptions{Limit: 20}})
	if err != nil {
		t.Fatalf("ListFiles = %v", err)
	}
	if total != 1 {
		t.Errorf("after incomplete sync total = %d, want 1 (rows kept)", total)
	}
}

func TestSyncLocalFilesPrunesPerRoot(t *testing.T) {
	rootA := t.TempDir()
	rootB := t.TempDir()
	st := newTestStore(t, rootA, rootB)
	var idA, idB int64
	if err := st.db.QueryRow(`SELECT id FROM roots WHERE path = ?`, rootA).Scan(&idA); err != nil {
		t.Fatalf("rootA id: %v", err)
	}
	if err := st.db.QueryRow(`SELECT id FROM roots WHERE path = ?`, rootB).Scan(&idB); err != nil {
		t.Fatalf("rootB id: %v", err)
	}
	keepA := rootA + "/keep.mp3"
	insertFile(t, st, idA, keepA, nil)
	insertFile(t, st, idB, rootB+"/gone.mp3", nil)

	// rootB walk incomplete: only rootA prunes (nothing stale there).
	stats, err := st.SyncLocalFiles(ctx(), []ScannedFile{{
		LibraryRoot: rootA, Path: keepA,
		SizeBytes: 100, MtimeNs: 123, FormatHint: "mp3",
	}}, "2024-06-01 00:00:00", SyncOptions{IncompleteRoots: []string{rootB}})
	if err != nil {
		t.Fatalf("SyncLocalFiles = %v", err)
	}
	if stats.Pruned != 0 {
		t.Errorf("pruned = %d, want 0 (rootB incomplete, rootA has no stale rows)", stats.Pruned)
	}
	if len(stats.KeptRoots) != 1 || stats.KeptRoots[0] != rootB {
		t.Errorf("KeptRoots = %v, want [%q]", stats.KeptRoots, rootB)
	}
	_, total, err := st.ListFiles(ctx(), FileListOptions{PageOptions: PageOptions{Limit: 20}})
	if err != nil {
		t.Fatalf("ListFiles = %v", err)
	}
	if total != 2 {
		t.Errorf("total = %d, want 2 (both roots kept)", total)
	}
}

func TestSyncLocalFilesToleratesUnreadableFile(t *testing.T) {
	root := t.TempDir()
	st := newTestStore(t, root)
	rootID := testRootID(t, st)

	// A tracked file with an intact row and matching on-disk state skips
	// the tag read, so its link survives even though the file is junk.
	good := root + "/good.mp3"
	if err := os.WriteFile(good, bytes.Repeat([]byte("a"), 100), 0o644); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(good)
	if err != nil {
		t.Fatal(err)
	}
	tracked := "t-123"
	if _, err := st.db.Exec(
		`INSERT INTO local_files (root_id, path, size_bytes, mtime_ns, format_hint, scanned_at, track_id)
		 VALUES (?, ?, ?, ?, 'mp3', '2024-01-01 00:00:00', ?)`,
		rootID, good, fi.Size(), fi.ModTime().UnixNano(), tracked,
	); err != nil {
		t.Fatalf("insert tracked file = %v", err)
	}

	// An unreadable file must not fail the sync.
	bad := root + "/bad.mp3"
	if err := os.WriteFile(bad, []byte("audio bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(bad, 0o000); err != nil {
		t.Fatal(err)
	}

	stats, err := st.SyncLocalFiles(ctx(), []ScannedFile{
		{LibraryRoot: root, Path: good, SizeBytes: fi.Size(), MtimeNs: fi.ModTime().UnixNano(), FormatHint: "mp3"},
		{LibraryRoot: root, Path: bad, SizeBytes: 17, MtimeNs: 1, FormatHint: "mp3"},
	}, "2024-06-01 00:00:00", SyncOptions{})
	if err != nil {
		t.Fatalf("SyncLocalFiles = %v", err)
	}
	if len(stats.Failed) != 1 || stats.Failed[0] != bad {
		t.Errorf("Failed = %v, want [%s]", stats.Failed, bad)
	}
	if stats.Upserted != 2 {
		t.Errorf("Upserted = %d, want 2 (the unreadable file still syncs)", stats.Upserted)
	}

	var trackID any
	if err := st.db.QueryRow(`SELECT track_id FROM local_files WHERE path = ?`, good).Scan(&trackID); err != nil {
		t.Fatalf("query good row = %v", err)
	}
	if trackID == nil || trackID.(string) != tracked {
		t.Errorf("good row track_id = %v, want preserved %q", trackID, tracked)
	}

	// The unreadable file's row was refreshed, so the prune keeps it.
	var n int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM local_files WHERE path = ?`, bad).Scan(&n); err != nil || n != 1 {
		t.Errorf("bad file row survived prune = %d rows, %v; want 1", n, err)
	}
}

func TestSyncLocalFilesSkipsUnchangedFiles(t *testing.T) {
	root := t.TempDir()
	st := newTestStore(t, root)
	rootID := testRootID(t, st)

	// Row matches the on-disk stat, so the scan must not re-read tags:
	// making the file unreadable with the same size and mtime is invisible.
	path := root + "/song.mp3"
	junk := bytes.Repeat([]byte("j"), 200)
	if err := os.WriteFile(path, junk, 0o644); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	mtime := fi.ModTime()
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(
		`INSERT INTO local_files (root_id, path, size_bytes, mtime_ns, format_hint, scanned_at)
		 VALUES (?, ?, ?, ?, 'mp3', '2024-01-01 00:00:00')`,
		rootID, path, fi.Size(), mtime.UnixNano(),
	); err != nil {
		t.Fatalf("insert file = %v", err)
	}

	scanned := ScannedFile{
		LibraryRoot: root, Path: path,
		SizeBytes: fi.Size(), MtimeNs: mtime.UnixNano(), FormatHint: "mp3",
	}
	stats, err := st.SyncLocalFiles(ctx(), []ScannedFile{scanned}, "2024-06-01 00:00:00", SyncOptions{})
	if err != nil {
		t.Fatalf("SyncLocalFiles = %v", err)
	}
	if len(stats.Failed) != 0 {
		t.Errorf("Failed = %v, want empty (unchanged file must skip the tag read)", stats.Failed)
	}

	// Control: make the file unreadable AND bump mtime, so the scan
	// re-reads it — and reports the failure.
	later := mtime.Add(time.Minute)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatal(err)
	}
	scanned.MtimeNs = later.UnixNano()
	stats, err = st.SyncLocalFiles(ctx(), []ScannedFile{scanned}, "2024-06-02 00:00:00", SyncOptions{})
	if err != nil {
		t.Fatalf("SyncLocalFiles control = %v", err)
	}
	if len(stats.Failed) != 1 || stats.Failed[0] != path {
		t.Errorf("control Failed = %v, want [%s]", stats.Failed, path)
	}
}

func TestLinkUnlink(t *testing.T) {
	root := t.TempDir()
	st := newTestStore(t, root)
	_ = st.CreateArtist(ctx(), Artist{ID: testArtistID, Name: "Test Artist"})
	if err := st.SaveDiscography(ctx(), []FullReleaseParams{testRelease(testTrackID, testRelID, testGroupID)}); err != nil {
		t.Fatalf("SaveDiscography = %v", err)
	}
	rootID := testRootID(t, st)
	fileID := insertFile(t, st, rootID, root+"/song.mp3", nil)

	if err := st.Link(ctx(), fileID, testTrackID, root+"/song.mp3", 200, 456); err != nil {
		t.Fatalf("Link = %v", err)
	}
	got, err := st.GetFile(ctx(), fileID, false)
	if err != nil {
		t.Fatalf("GetFile = %v", err)
	}
	if got.TrackID == nil || *got.TrackID != testTrackID {
		t.Errorf("track = %v, want %q", got.TrackID, testTrackID)
	}

	if err := st.Unlink(ctx(), fileID); err != nil {
		t.Fatalf("Unlink = %v", err)
	}
	got, _ = st.GetFile(ctx(), fileID, false)
	if got.TrackID != nil {
		t.Errorf("after unlink track = %v, want nil", *got.TrackID)
	}

	// Errors.
	if err := st.Link(ctx(), 999999, testTrackID, root+"/x.mp3", 1, 1); !apperrors.Is(err, apperrors.ErrNotFound) {
		t.Errorf("missing file err = %v, want NOT_FOUND", err)
	}
	if err := st.Link(ctx(), fileID, "00000000-0000-4000-8000-000000000000", root+"/song.mp3", 1, 1); !apperrors.Is(err, apperrors.ErrNotFound) {
		t.Errorf("missing track err = %v, want NOT_FOUND", err)
	}
	if err := st.Link(ctx(), fileID, testTrackID, "  ", 1, 1); err == nil {
		t.Error("empty path = nil, want error")
	}

	// Conflict: second file already at destination.
	other := insertFile(t, st, rootID, root+"/other.mp3", nil)
	if err := st.Link(ctx(), other, testTrackID, root+"/song.mp3", 1, 1); !apperrors.Is(err, apperrors.ErrAlreadyExists) {
		t.Errorf("conflict err = %v, want ALREADY_EXISTS", err)
	}

	if _, err := st.GetFile(ctx(), 999999, false); !apperrors.Is(err, apperrors.ErrNotFound) {
		t.Errorf("missing file err = %v, want NOT_FOUND", err)
	}
}

func TestListFilesFiltering(t *testing.T) {
	root := t.TempDir()
	st := newTestStore(t, root)
	_ = st.CreateArtist(ctx(), Artist{ID: testArtistID, Name: "Test Artist"})
	if err := st.SaveDiscography(ctx(), []FullReleaseParams{testRelease(testTrackID, testRelID, testGroupID)}); err != nil {
		t.Fatalf("SaveDiscography = %v", err)
	}
	rootID := testRootID(t, st)
	track := testTrackID
	insertFile(t, st, rootID, root+"/alpha.mp3", &track)
	insertFile(t, st, rootID, root+"/beta.flac", nil)

	linked := true
	files, total, err := st.ListFiles(ctx(), FileListOptions{
		PageOptions: PageOptions{Limit: 20}, Linked: &linked, IncludeTrack: true,
	})
	if err != nil {
		t.Fatalf("ListFiles linked = %v", err)
	}
	if total != 1 || files[0].Track == nil {
		t.Errorf("linked total=%d track=%+v, want 1 with track", total, files)
	}

	unlinked := false
	_, total, _ = st.ListFiles(ctx(), FileListOptions{
		PageOptions: PageOptions{Limit: 20}, Linked: &unlinked,
	})
	if total != 1 {
		t.Errorf("unlinked total = %d, want 1", total)
	}

	_, total, _ = st.ListFiles(ctx(), FileListOptions{
		SearchOptions: SearchOptions{Query: "alpha"},
		PageOptions:   PageOptions{Limit: 20},
	})
	if total != 1 {
		t.Errorf("query total = %d, want 1", total)
	}

	_, total, _ = st.ListFiles(ctx(), FileListOptions{
		PageOptions: PageOptions{Limit: 20}, Format: "FLAC",
	})
	if total != 1 {
		t.Errorf("format total = %d, want 1 (case-insensitive)", total)
	}
}

func TestFileStats(t *testing.T) {
	root := t.TempDir()
	st := newTestStore(t, root)
	rootID := testRootID(t, st)
	track := testTrackID
	insertFile(t, st, rootID, root+"/a.mp3", &track)
	insertFile(t, st, rootID, root+"/b.flac", nil)

	stats, err := st.GetLocalFilesStats(ctx())
	if err != nil {
		t.Fatalf("GetLocalFilesStats = %v", err)
	}
	if stats.Total != 2 || stats.Linked != 1 || stats.Unlinked != 1 {
		t.Errorf("stats = %+v", stats)
	}
	if stats.Formats["mp3"] != 1 || stats.Formats["flac"] != 1 {
		t.Errorf("formats = %v", stats.Formats)
	}

	lib, err := st.GetLibraryStats(ctx())
	if err != nil {
		t.Fatalf("GetLibraryStats = %v", err)
	}
	if lib.Files.Total != 2 {
		t.Errorf("lib files = %d, want 2", lib.Files.Total)
	}

	if _, err := st.GetRoot(ctx(), rootID); err != nil {
		t.Errorf("GetRoot = %v", err)
	}
	if _, err := st.GetRoot(ctx(), 999999); !apperrors.Is(err, apperrors.ErrNotFound) {
		t.Errorf("missing root err = %v, want NOT_FOUND", err)
	}
}

func TestCleanupOrphanCredits(t *testing.T) {
	st := newTestStore(t)
	_ = st.CreateArtist(ctx(), Artist{ID: testArtistID, Name: "Test Artist"})
	if err := st.SaveDiscography(ctx(), []FullReleaseParams{testRelease(testTrackID, testRelID, testGroupID)}); err != nil {
		t.Fatalf("SaveDiscography = %v", err)
	}
	if err := st.DeleteReleaseGroup(ctx(), testGroupID, false); err != nil {
		t.Fatalf("DeleteReleaseGroup = %v", err)
	}
	n, err := st.CleanupOrphanCredits(ctx())
	if err != nil {
		t.Fatalf("CleanupOrphanCredits = %v", err)
	}
	_ = n
}

func TestIsUniqueViolation(t *testing.T) {
	root := t.TempDir()
	st := newTestStore(t, root)
	rootID := testRootID(t, st)
	if _, err := st.db.Exec(
		`INSERT INTO local_files (root_id, path, size_bytes, mtime_ns, format_hint, scanned_at)
		 VALUES (?, '/root/a.mp3', 1, 1, 'mp3', '2024-01-01 00:00:00')`, rootID,
	); err != nil {
		t.Fatalf("insert = %v", err)
	}
	_, err := st.db.Exec(
		`INSERT INTO local_files (root_id, path, size_bytes, mtime_ns, format_hint, scanned_at)
		 VALUES (?, '/root/a.mp3', 1, 1, 'mp3', '2024-01-01 00:00:00')`, rootID,
	)
	if err == nil {
		t.Fatal("duplicate insert = nil error, want a UNIQUE violation")
	}
	if !isUniqueViolation(err) {
		t.Errorf("isUniqueViolation(%v) = false, want true (driver code mismatch)", err)
	}
	if isUniqueViolation(nil) || isUniqueViolation(errors.New("some other error")) {
		t.Error("non-sqlite errors must not classify as unique violations")
	}
}
