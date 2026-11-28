package tasks

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ppablomunoz/coda/internal/artwork"
	"github.com/ppablomunoz/coda/internal/store"
	"github.com/ppablomunoz/coda/internal/utils"
)

const (
	gcLiveRel    = "11111111-1111-4111-8111-111111111111"
	gcOrphanRel  = "22222222-2222-4222-8222-222222222222"
	gcTombRel    = "33333333-3333-4333-8333-333333333333"
	gcStaleRel   = "44444444-4444-4334-8444-444444444444"
	gcLiveArtist = "55555555-5555-4555-8555-555555555555"
	gcOrphanArt  = "66666666-6666-4666-8666-666666666666"
	gcTombArtist = "77777777-7777-4777-8777-777777777777"
	gcGroupLive  = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	gcGroupTomb  = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	gcGroupStale = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
)

func seedGCRel(t *testing.T, st *store.Store, artistID, groupID, relID string) {
	t.Helper()
	ctx := context.Background()
	if err := st.CreateArtist(ctx, store.Artist{ID: artistID, Name: "GC Artist"}); err != nil {
		t.Fatalf("CreateArtist = %v", err)
	}
	credit := []store.CreditEntry{{ArtistID: artistID, Name: "GC Artist", CreditedName: "GC Artist"}}
	if err := st.SaveDiscography(ctx, []store.FullReleaseParams{{
		ID: relID, Title: "GC Release", Credit: credit,
		Group: store.ReleaseGroupParams{ID: groupID, Title: "GC Group", Type: "album", Credit: credit},
		Tracks: []store.TrackParams{{
			ID: "bbbbbbbb-bbbb-4bbb-8bbb-" + relID[24:], ReleaseID: relID,
			Disc: 1, Position: 1, Number: "1", Title: "GC Track",
			Credit:      credit,
			RecordingID: "cccccccc-cccc-4ccc-8ccc-cccccccccccc",
		}},
	}}); err != nil {
		t.Fatalf("SaveDiscography = %v", err)
	}
}

func writeGCFile(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("img"), 0o644); err != nil {
		t.Fatalf("write %s = %v", name, err)
	}
}

func TestPruneOrphanCovers(t *testing.T) {
	ctx := context.Background()
	log := utils.NewWithWriter(io.Discard)
	st, err := store.NewStore(filepath.Join(t.TempDir(), "test.db"), nil, log)
	if err != nil {
		t.Fatalf("NewStore = %v", err)
	}
	t.Cleanup(st.Close)
	coverDir := t.TempDir()

	// Live release cover: row + matching file stay.
	seedGCRel(t, st, "a0a0a0a0-a0a0-4a0a-8a0a-a0a0a0a0a0a0", gcGroupLive, gcLiveRel)
	if err := SaveCover(ctx, st, coverDir, gcLiveRel, &artwork.Result{
		Bytes: embedPNG, MIME: "image/png", Source: artwork.SourceUpload,
	}); err != nil {
		t.Fatalf("SaveCover live = %v", err)
	}

	// Orphan release file: no row.
	writeGCFile(t, coverDir, gcOrphanRel+".jpg")

	// Tombstoned release: row removed, file stays until GC.
	seedGCRel(t, st, "b0b0b0b0-b0b0-4b0b-8b0b-b0b0b0b0b0b0", gcGroupTomb, gcTombRel)
	if err := SaveCover(ctx, st, coverDir, gcTombRel, &artwork.Result{
		Bytes: embedPNG, MIME: "image/png", Source: artwork.SourceUpload,
	}); err != nil {
		t.Fatalf("SaveCover tomb = %v", err)
	}
	if err := st.MarkCoverRemoved(ctx, gcTombRel); err != nil {
		t.Fatalf("MarkCoverRemoved = %v", err)
	}

	// Stale sibling: the row is a normalized JPEG, a planted png sibling
	// from an earlier MIME goes, the jpg stays.
	seedGCRel(t, st, "c0c0c0c0-c0c0-4c0c-8c0c-c0c0c0c0c0c0", gcGroupStale, gcStaleRel)
	if err := SaveCover(ctx, st, coverDir, gcStaleRel, &artwork.Result{
		Bytes: embedPNG, MIME: "image/png", Source: artwork.SourceUpload,
	}); err != nil {
		t.Fatalf("SaveCover stale = %v", err)
	}
	writeGCFile(t, coverDir, gcStaleRel+".png")

	// Live artist photo stays.
	if err := st.CreateArtist(ctx, store.Artist{ID: gcLiveArtist, Name: "Live"}); err != nil {
		t.Fatalf("CreateArtist live = %v", err)
	}
	if err := SaveArtistCover(ctx, st, coverDir, gcLiveArtist, &artwork.Result{
		Bytes: embedPNG, MIME: "image/png", Source: artwork.SourceUpload,
	}); err != nil {
		t.Fatalf("SaveArtistCover live = %v", err)
	}

	// Orphan artist file goes.
	writeGCFile(t, coverDir, "artist-"+gcOrphanArt+".jpg")

	// Tombstoned artist photo goes.
	if err := st.CreateArtist(ctx, store.Artist{ID: gcTombArtist, Name: "Tomb"}); err != nil {
		t.Fatalf("CreateArtist tomb = %v", err)
	}
	if err := SaveArtistCover(ctx, st, coverDir, gcTombArtist, &artwork.Result{
		Bytes: embedPNG, MIME: "image/png", Source: artwork.SourceUpload,
	}); err != nil {
		t.Fatalf("SaveArtistCover tomb = %v", err)
	}
	if err := st.MarkArtistCoverRemoved(ctx, gcTombArtist); err != nil {
		t.Fatalf("MarkArtistCoverRemoved = %v", err)
	}

	// Unknown files are never touched.
	writeGCFile(t, coverDir, "notes.txt")
	writeGCFile(t, coverDir, "not-a-uuid.jpg")

	// Cover temp files: old goes, fresh stays.
	oldTmp := filepath.Join(coverDir, ".coda-cover-"+gcLiveRel+"-old.tmp")
	if err := os.WriteFile(oldTmp, []byte("x"), 0o644); err != nil {
		t.Fatalf("write old tmp = %v", err)
	}
	ago := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(oldTmp, ago, ago); err != nil {
		t.Fatalf("chtimes = %v", err)
	}
	freshTmp := filepath.Join(coverDir, ".coda-cover-"+gcLiveRel+"-fresh.tmp")
	writeGCFile(t, coverDir, ".coda-cover-"+gcLiveRel+"-fresh.tmp")
	_ = freshTmp

	n := PruneOrphanCovers(ctx, st, coverDir, log)
	if n != 6 {
		t.Errorf("removed = %d, want 6 (orphan rel, tomb rel, stale sib, orphan art, tomb art, old tmp)", n)
	}

	kept := []string{
		gcLiveRel + ".jpg",
		gcStaleRel + ".jpg",
		"artist-" + gcLiveArtist + ".jpg",
		"notes.txt",
		"not-a-uuid.jpg",
		".coda-cover-" + gcLiveRel + "-fresh.tmp",
	}
	for _, k := range kept {
		if _, err := os.Stat(filepath.Join(coverDir, k)); err != nil {
			t.Errorf("kept file %q missing: %v", k, err)
		}
	}
	gone := []string{
		gcOrphanRel + ".jpg",
		gcTombRel + ".jpg",
		gcStaleRel + ".png",
		"artist-" + gcOrphanArt + ".jpg",
		"artist-" + gcTombArtist + ".jpg",
		".coda-cover-" + gcLiveRel + "-old.tmp",
	}
	for _, k := range gone {
		if _, err := os.Stat(filepath.Join(coverDir, k)); !os.IsNotExist(err) {
			t.Errorf("orphan file %q still present", k)
		}
	}

	// Second run is a no-op.
	if n := PruneOrphanCovers(ctx, st, coverDir, log); n != 0 {
		t.Errorf("second run removed = %d, want 0", n)
	}
}

func TestPruneOrphanCoversMissingDir(t *testing.T) {
	log := utils.NewWithWriter(io.Discard)
	st, err := store.NewStore(filepath.Join(t.TempDir(), "test.db"), nil, log)
	if err != nil {
		t.Fatalf("NewStore = %v", err)
	}
	t.Cleanup(st.Close)
	if n := PruneOrphanCovers(context.Background(), st, filepath.Join(t.TempDir(), "nope"), log); n != 0 {
		t.Errorf("missing dir removed = %d, want 0", n)
	}
	if n := PruneOrphanCovers(context.Background(), st, "", log); n != 0 {
		t.Errorf("empty dir removed = %d, want 0", n)
	}
}

// TestPruneOrphanCoversAfterDeleteArtist covers the issue scenario:
// deleting an artist cascades rows but leaves files; GC reaps them.
// Foreign keys are enabled (like production, via the DSN pragma) so the
// cascade actually fires — the default test DSN leaves FKs off.
func TestPruneOrphanCoversAfterDeleteArtist(t *testing.T) {
	ctx := context.Background()
	log := utils.NewWithWriter(io.Discard)
	dbPath := filepath.Join(t.TempDir(), "test.db")
	st, err := store.NewStore(dbPath+"?_pragma=foreign_keys(ON)", nil, log)
	if err != nil {
		t.Fatalf("NewStore = %v", err)
	}
	t.Cleanup(st.Close)
	coverDir := t.TempDir()

	artistID := "90909090-9090-4909-8909-090909090909"
	relID := "91919191-9191-4919-8919-191919191919"
	groupID := "92929292-9292-4929-8929-292929292929"
	seedGCRel(t, st, artistID, groupID, relID)
	if err := SaveCover(ctx, st, coverDir, relID, &artwork.Result{
		Bytes: embedPNG, MIME: "image/jpeg", Source: artwork.SourceUpload,
	}); err != nil {
		t.Fatalf("SaveCover = %v", err)
	}
	if err := SaveArtistCover(ctx, st, coverDir, artistID, &artwork.Result{
		Bytes: embedPNG, MIME: "image/jpeg", Source: artwork.SourceUpload,
	}); err != nil {
		t.Fatalf("SaveArtistCover = %v", err)
	}

	if _, err := st.DeleteArtist(ctx, artistID, false); err != nil {
		t.Fatalf("DeleteArtist = %v", err)
	}
	// Release row cascaded, file leaked.
	if _, err := st.GetCover(ctx, relID); err == nil {
		t.Fatal("cover row survived DeleteArtist")
	}
	relFile := filepath.Join(coverDir, relID+".jpg")
	if _, err := os.Stat(relFile); err != nil {
		t.Fatalf("release file gone before GC: %v", err)
	}
	// Soft-deleted artists keep their photo row: the file is still live.
	artFile := filepath.Join(coverDir, "artist-"+artistID+".jpg")
	if _, err := st.GetArtistCover(ctx, artistID); err != nil {
		t.Fatalf("artist cover row should survive soft-delete: %v", err)
	}

	if n := PruneOrphanCovers(ctx, st, coverDir, log); n != 1 {
		t.Errorf("removed = %d, want 1 (release file only)", n)
	}
	if _, err := os.Stat(relFile); !os.IsNotExist(err) {
		t.Error("release file still present after GC")
	}
	if _, err := os.Stat(artFile); err != nil {
		t.Errorf("artist file wrongly removed: %v", err)
	}
}
