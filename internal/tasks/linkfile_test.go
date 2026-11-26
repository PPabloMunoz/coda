package tasks

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	apperrors "github.com/ppablomunoz/coda/internal/errors"
	"github.com/ppablomunoz/coda/internal/store"
)

// syncWavFile writes a minimal WAV at root/name and syncs it into the store
// without linking, returning its file id and path.
func syncWavFile(t *testing.T, st *store.Store, root, name string) (int64, string) {
	t.Helper()
	path := filepath.Join(root, name)
	writeTestWav(t, path)
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat = %v", err)
	}
	if _, err := st.SyncLocalFiles(context.Background(), []store.ScannedFile{{
		LibraryRoot: root, Path: path,
		SizeBytes: fi.Size(), MtimeNs: fi.ModTime().UnixNano(), FormatHint: "wav",
	}}, "2024-01-01 00:00:00"); err != nil {
		t.Fatalf("SyncLocalFiles = %v", err)
	}
	files, _, err := st.ListFiles(context.Background(), store.FileListOptions{PageOptions: store.PageOptions{Limit: 100}})
	if err != nil {
		t.Fatalf("ListFiles = %v", err)
	}
	for _, f := range files {
		if f.Path == path {
			return f.ID, f.Path
		}
	}
	t.Fatalf("synced file %q not found", path)
	return 0, ""
}

func fileIDRow(t *testing.T, st *store.Store, id int64) store.LocalFile {
	t.Helper()
	f, err := st.GetFile(context.Background(), id, false)
	if err != nil {
		t.Fatalf("GetFile(%d) = %v", id, err)
	}
	return *f
}

func TestMoveFileNoReplaceSuccess(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src", "song.wav")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "dst", "nested", "song.wav")

	if err := moveFileNoReplace("test", 1, "track", src, dst); err != nil {
		t.Fatalf("move = %v", err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("src still present: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read dst = %v", err)
	}
	if string(got) != "audio" {
		t.Errorf("dst = %q, want audio", got)
	}
}

func TestMoveFileNoReplaceSamePathNoop(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "song.wav")
	if err := os.WriteFile(path, []byte("audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := moveFileNoReplace("test", 1, "track", path, path); err != nil {
		t.Fatalf("same path = %v, want nil", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "audio" {
		t.Errorf("content = %q, want audio", got)
	}
}

func TestMoveFileNoReplaceOccupied(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.wav")
	dst := filepath.Join(dir, "dst.wav")
	if err := os.WriteFile(src, []byte("src"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("dst"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := moveFileNoReplace("test", 1, "track", src, dst); !apperrors.Is(err, apperrors.ErrAlreadyExists) {
		t.Fatalf("occupied = %v, want ALREADY_EXISTS", err)
	}
	if got, _ := os.ReadFile(src); string(got) != "src" {
		t.Errorf("src = %q, want untouched", got)
	}
	if got, _ := os.ReadFile(dst); string(got) != "dst" {
		t.Errorf("dst = %q, want untouched", got)
	}
}

func TestCopyFileNoReplaceSuccessAndOccupied(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.wav")
	dst := filepath.Join(dir, "sub", "dst.wav")
	if err := os.WriteFile(src, []byte("audio"), 0o640); err != nil {
		t.Fatal(err)
	}

	if err := copyFileNoReplace("test", 1, "track", src, dst); err != nil {
		t.Fatalf("copy = %v", err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("src still present after copy-move: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil || string(got) != "audio" {
		t.Fatalf("dst = %q, %v; want audio", got, err)
	}
	if fi, _ := os.Stat(dst); fi.Mode().Perm() != 0o640 {
		t.Errorf("dst mode = %v, want 0640 preserved", fi.Mode())
	}

	src2 := filepath.Join(dir, "src2.wav")
	if err := os.WriteFile(src2, []byte("src2"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := copyFileNoReplace("test", 2, "track", src2, dst); !apperrors.Is(err, apperrors.ErrAlreadyExists) {
		t.Fatalf("occupied copy = %v, want ALREADY_EXISTS", err)
	}
	if got, _ := os.ReadFile(src2); string(got) != "src2" {
		t.Errorf("src2 = %q, want untouched", got)
	}
	if got, _ := os.ReadFile(dst); string(got) != "audio" {
		t.Errorf("dst = %q, want untouched", got)
	}
}

func canonicalDst(t *testing.T, st *store.Store, root, trackID, ext string) string {
	t.Helper()
	rel, track, err := st.GetTrackFullRelease(context.Background(), trackID)
	if err != nil {
		t.Fatalf("GetTrackFullRelease = %v", err)
	}
	dst, err := LibraryTargetPath(root, LinkInfoFromStore(rel, track), ext)
	if err != nil {
		t.Fatalf("LibraryTargetPath = %v", err)
	}
	return dst
}

func TestLinkFileOccupiedLeavesSourceUntouched(t *testing.T) {
	root := t.TempDir()
	r, st := newEmbedRunner(t, root)
	seedEmbedRelease(t, st)
	ctx := context.Background()

	id, path := syncWavFile(t, st, root, "song.wav")
	orig, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	dst := canonicalDst(t, st, root, embedTrackID, "wav")
	if dst == path {
		t.Fatal("test setup: dst equals src, want distinct paths")
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("occupant"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, _, err := LinkFile(ctx, st, r.coverDir, id, embedTrackID); !apperrors.Is(err, apperrors.ErrAlreadyExists) {
		t.Fatalf("LinkFile occupied = %v, want ALREADY_EXISTS", err)
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, orig) {
		t.Error("source file was modified by a failed link")
	}
	if got, _ := os.ReadFile(dst); string(got) != "occupant" {
		t.Errorf("occupant = %q, want untouched", got)
	}
	row := fileIDRow(t, st, id)
	if row.TrackID != nil {
		t.Errorf("row linked to %q, want unlinked after 409", *row.TrackID)
	}
	if row.Path != path {
		t.Errorf("row path = %q, want %q", row.Path, path)
	}
}

func TestLinkFileConcurrentSameDestination(t *testing.T) {
	root := t.TempDir()
	r, st := newEmbedRunner(t, root)
	seedEmbedRelease(t, st)
	ctx := context.Background()

	idA, pathA := syncWavFile(t, st, root, "a.wav")
	idB, pathB := syncWavFile(t, st, root, "b.wav")
	origA, _ := os.ReadFile(pathA)
	origB, _ := os.ReadFile(pathB)

	type result struct {
		linked, moved bool
		err           error
	}
	results := make([]result, 2)
	var wg sync.WaitGroup
	for i, id := range []int64{idA, idB} {
		wg.Add(1)
		go func(i int, id int64) {
			defer wg.Done()
			linked, moved, err := LinkFile(ctx, st, r.coverDir, id, embedTrackID)
			results[i] = result{linked, moved, err}
		}(i, id)
	}
	wg.Wait()

	var success, conflict int
	for _, res := range results {
		switch {
		case res.err == nil:
			success++
			if !res.linked || !res.moved {
				t.Errorf("success = linked=%t moved=%t, want both true", res.linked, res.moved)
			}
		case apperrors.Is(res.err, apperrors.ErrAlreadyExists):
			conflict++
		default:
			t.Errorf("unexpected error: %v", res.err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("success=%d conflict=%d, want exactly one of each", success, conflict)
	}

	// No lost file: the winner sits at the canonical path, the loser is
	// still at its source path with its bytes and DB row untouched.
	rowA := fileIDRow(t, st, idA)
	rowB := fileIDRow(t, st, idB)
	var winner, loser store.LocalFile
	var loserOrig []byte
	if rowA.TrackID != nil {
		winner, loser, loserOrig = rowA, rowB, origB
	} else if rowB.TrackID != nil {
		winner, loser, loserOrig = rowB, rowA, origA
	} else {
		t.Fatalf("neither file linked after race (A=%+v B=%+v)", rowA, rowB)
	}
	if loser.TrackID != nil {
		t.Errorf("loser row linked to %q, want unlinked", *loser.TrackID)
	}
	if _, err := os.Stat(winner.Path); err != nil {
		t.Errorf("winner file missing at %q: %v", winner.Path, err)
	}
	got, err := os.ReadFile(loser.Path)
	if err != nil {
		t.Fatalf("loser file missing at %q: %v", loser.Path, err)
	}
	if !bytes.Equal(got, loserOrig) {
		t.Error("loser source file was modified by the failed link")
	}
}

func TestLinkFileAlreadyCurrentIsNoop(t *testing.T) {
	root := t.TempDir()
	r, st := newEmbedRunner(t, root)
	seedEmbedRelease(t, st)
	ctx := context.Background()

	id, _ := syncWavFile(t, st, root, "song.wav")
	linked, _, err := LinkFile(ctx, st, r.coverDir, id, embedTrackID)
	if err != nil || !linked {
		t.Fatalf("first LinkFile = linked=%t, %v; want linked=true", linked, err)
	}
	row := fileIDRow(t, st, id)
	before, err := os.ReadFile(row.Path)
	if err != nil {
		t.Fatal(err)
	}

	linked, moved, err := LinkFile(ctx, st, r.coverDir, id, embedTrackID)
	if err != nil {
		t.Fatalf("second LinkFile = %v, want nil", err)
	}
	if linked || moved {
		t.Errorf("second LinkFile = linked=%t moved=%t, want no-op", linked, moved)
	}
	after, err := os.ReadFile(fileIDRow(t, st, id).Path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("no-op link rewrote the file, want it untouched")
	}
}
