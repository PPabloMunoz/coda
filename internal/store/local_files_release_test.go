package store

import (
	"testing"
)

func strptr(s string) *string { return &s }

func TestListFilesByRelease(t *testing.T) {
	root := t.TempDir()
	st := newTestStore(t, root)
	rootID := testRootID(t, st)

	if err := st.CreateArtist(ctx(), Artist{ID: testArtistID, Name: "Test Artist"}); err != nil {
		t.Fatalf("CreateArtist = %v", err)
	}
	rel1 := testRelease(testTrackID, testRelID, testGroupID)
	rel2 := testRelease(testTrackID2, testRelID2, testGroupEP)
	rel2.Title = "Second Release"
	if err := st.SaveDiscography(ctx(), []FullReleaseParams{rel1, rel2}); err != nil {
		t.Fatalf("SaveDiscography = %v", err)
	}

	f1 := insertFile(t, st, rootID, root+"/one.mp3", strptr(testTrackID))
	f2 := insertFile(t, st, rootID, root+"/two.mp3", strptr(testTrackID))
	insertFile(t, st, rootID, root+"/other-release.mp3", strptr(testTrackID2))
	insertFile(t, st, rootID, root+"/unlinked.mp3", nil)

	got, err := st.ListFilesByRelease(ctx(), testRelID)
	if err != nil {
		t.Fatalf("ListFilesByRelease = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("files = %d, want 2", len(got))
	}
	if got[0].ID != f1 || got[1].ID != f2 {
		t.Errorf("ids = [%d %d], want [%d %d]", got[0].ID, got[1].ID, f1, f2)
	}
	for _, f := range got {
		if f.TrackID == nil || *f.TrackID != testTrackID {
			t.Errorf("file %d track = %v, want %q", f.ID, f.TrackID, testTrackID)
		}
	}

	// Unknown release: no rows, no error.
	empty, err := st.ListFilesByRelease(ctx(), "00000000-0000-4000-8000-000000000000")
	if err != nil {
		t.Fatalf("unknown release = %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("unknown release files = %d, want 0", len(empty))
	}
}
