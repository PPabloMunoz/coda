package tasks

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ppablomunoz/coda/internal/store"
	"github.com/ppablomunoz/coda/internal/tags"
)

// TestAutoLinkPicksUpNewImport is the #46 acceptance: a file whose tag MBID
// has no track yet stays unlinked, then auto-links after the import lands
// without re-reading tags in the sync (evidence drives the candidate).
func TestAutoLinkPicksUpNewImport(t *testing.T) {
	root := t.TempDir()
	r, st := newEmbedRunner(t, root)
	ctx := context.Background()

	trackID := "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	path := filepath.Join(root, "song.wav")
	writeTestWav(t, path)
	if err := tags.WriteTags(path, tags.ToMap(&tags.Snapshot{
		Title: "New Song", Album: "New Release", ArtistRaw: "New Artist",
		TrackNumber: 1, DiscNumber: 1, MBReleaseTrackID: trackID,
	})); err != nil {
		t.Fatalf("WriteTags = %v", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SyncLocalFiles(ctx, []store.ScannedFile{{
		LibraryRoot: root, Path: path,
		SizeBytes: fi.Size(), MtimeNs: fi.ModTime().UnixNano(), FormatHint: "wav",
	}}, "2024-01-01 00:00:00", store.SyncOptions{}); err != nil {
		t.Fatalf("SyncLocalFiles = %v", err)
	}

	// Unresolved: no candidate, no link.
	if cands, err := st.ListAutoLinkCandidates(ctx, 0, 100); err != nil {
		t.Fatalf("candidates = %v", err)
	} else if len(cands) != 0 {
		t.Fatalf("candidates before import = %v, want none", cands)
	}
	if got := r.autoLinkMoves(ctx); got.Linked != 0 {
		t.Fatalf("autolink before import = %+v, want 0 linked", got)
	}

	// Import a release containing the track: the same file becomes a
	// candidate and auto-links (move + tag rewrite) on the next pass.
	artistID := "bbbbbbbb-cccc-4ddd-8eee-ffffffffffff"
	groupID := "cccccccc-dddd-4eee-8fff-111111111111"
	relID := "dddddddd-eeee-4fff-8000-222222222222"
	if err := st.CreateArtist(ctx, store.Artist{ID: artistID, Name: "New Artist"}); err != nil {
		t.Fatalf("CreateArtist = %v", err)
	}
	credit := []store.CreditEntry{{ArtistID: artistID, Name: "New Artist", CreditedName: "New Artist"}}
	if err := st.SaveDiscography(ctx, []store.FullReleaseParams{{
		ID: relID, Title: "New Release", Credit: credit,
		Group: store.ReleaseGroupParams{ID: groupID, Title: "New Group", Type: "album", Credit: credit},
		Tracks: []store.TrackParams{{
			ID: trackID, ReleaseID: relID, Disc: 1, Position: 1, Number: "1",
			Title: "New Song", Credit: credit, RecordingID: "eeeeeeee-ffff-4000-8000-333333333333",
		}},
	}}); err != nil {
		t.Fatalf("SaveDiscography = %v", err)
	}

	cands, err := st.ListAutoLinkCandidates(ctx, 0, 100)
	if err != nil {
		t.Fatalf("candidates after import = %v", err)
	}
	if len(cands) != 1 {
		t.Fatalf("candidates after import = %v, want 1", cands)
	}
	got := r.autoLinkMoves(ctx)
	if got.Linked != 1 {
		t.Fatalf("autolink after import = %+v, want 1 linked", got)
	}
	// Linked rows leave the candidate set.
	if cands, err := st.ListAutoLinkCandidates(ctx, 0, 100); err != nil {
		t.Fatalf("candidates after link = %v", err)
	} else if len(cands) != 0 {
		t.Fatalf("candidates after link = %v, want none (already linked)", cands)
	}
}
