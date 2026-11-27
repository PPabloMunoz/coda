package store

import (
	"testing"

	apperrors "github.com/ppablomunoz/coda/internal/errors"
)

func TestSaveAndGetRelease(t *testing.T) {
	st := newTestStore(t)
	_ = st.CreateArtist(ctx(), Artist{ID: testArtistID, Name: "Test Artist"})
	if err := st.SaveDiscography(ctx(), []FullReleaseParams{testRelease(testTrackID, testRelID, testGroupID)}); err != nil {
		t.Fatalf("SaveDiscography = %v", err)
	}

	got, err := st.GetRelease(ctx(), testRelID, false)
	if err != nil {
		t.Fatalf("GetRelease = %v", err)
	}
	if got.Title != "Test Release" || got.Group.ID != testGroupID {
		t.Errorf("got %+v", got)
	}
	if len(got.Tracks) != 1 || got.Tracks[0].ID != testTrackID {
		t.Errorf("tracks = %+v", got.Tracks)
	}
	if len(got.Credit) != 1 || got.Credit[0].ArtistID != testArtistID {
		t.Errorf("credit = %+v", got.Credit)
	}

	// Missing release.
	if _, err := st.GetRelease(ctx(), "00000000-0000-4000-8000-000000000000", false); !apperrors.Is(err, apperrors.ErrNotFound) {
		t.Errorf("missing release err = %v, want NOT_FOUND", err)
	}
	if _, err := st.GetReleaseOfGroup(ctx(), "00000000-0000-4000-8000-000000000000", false); !apperrors.Is(err, apperrors.ErrNotFound) {
		t.Errorf("missing group release err = %v, want NOT_FOUND", err)
	}
}

func TestGetReleaseOfGroup(t *testing.T) {
	st := newTestStore(t)
	_ = st.CreateArtist(ctx(), Artist{ID: testArtistID, Name: "Test Artist"})
	if err := st.SaveDiscography(ctx(), []FullReleaseParams{testRelease(testTrackID, testRelID, testGroupID)}); err != nil {
		t.Fatalf("SaveDiscography = %v", err)
	}
	got, err := st.GetReleaseOfGroup(ctx(), testGroupID, false)
	if err != nil {
		t.Fatalf("GetReleaseOfGroup = %v", err)
	}
	if got.ID != testRelID {
		t.Errorf("release id = %q, want %q", got.ID, testRelID)
	}
}

func TestSaveDiscographySkipsInvalid(t *testing.T) {
	st := newTestStore(t)
	_ = st.CreateArtist(ctx(), Artist{ID: testArtistID, Name: "Test Artist"})

	bad := testRelease(testTrackID, testRelID, testGroupID)
	bad.Tracks = append(bad.Tracks, TrackParams{
		ID: "99999999-0000-4000-8000-000000000001", ReleaseID: testRelID,
		Disc: 1, Position: 0, Number: "0", Title: "",
	})
	// Duplicate group in same batch: second is skipped.
	dup := testRelease(testTrackID2, testRelID2, testGroupID)
	dup.Title = "Second"
	if err := st.SaveDiscography(ctx(), []FullReleaseParams{bad, dup}); err != nil {
		t.Fatalf("SaveDiscography = %v", err)
	}
	got, err := st.GetRelease(ctx(), testRelID, false)
	if err != nil {
		t.Fatalf("GetRelease = %v", err)
	}
	if len(got.Tracks) != 1 {
		t.Errorf("tracks = %d, want 1 (invalid skipped)", len(got.Tracks))
	}
	// Duplicate group skipped, so second release never saved.
	if _, err := st.GetRelease(ctx(), testRelID2, false); !apperrors.Is(err, apperrors.ErrNotFound) {
		t.Errorf("dup group release should be skipped, got %v", err)
	}
}

func TestNormalizeRGType(t *testing.T) {
	for in, want := range map[string]string{
		"Album": "album", "EP": "ep", "single": "single",
		"": "other", "bogus": "other", " ALBUM ": "album",
	} {
		if got := normalizeRGType(in); got != want {
			t.Errorf("normalizeRGType(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestReplaceRelease(t *testing.T) {
	st := newTestStore(t)
	_ = st.CreateArtist(ctx(), Artist{ID: testArtistID, Name: "Test Artist"})
	if err := st.SaveDiscography(ctx(), []FullReleaseParams{testRelease(testTrackID, testRelID, testGroupID)}); err != nil {
		t.Fatalf("SaveDiscography = %v", err)
	}

	replacement := testRelease(testTrackID2, testRelID2, testGroupID)
	replacement.Title = "Replacement"
	if err := st.ReplaceRelease(ctx(), testGroupID, replacement); err != nil {
		t.Fatalf("ReplaceRelease = %v", err)
	}
	if _, err := st.GetRelease(ctx(), testRelID, false); !apperrors.Is(err, apperrors.ErrNotFound) {
		t.Errorf("old release should be gone: %v", err)
	}
	got, err := st.GetRelease(ctx(), testRelID2, false)
	if err != nil {
		t.Fatalf("GetRelease new = %v", err)
	}
	if got.Title != "Replacement" {
		t.Errorf("title = %q", got.Title)
	}

	// Group mismatch.
	bad := testRelease(testTrackID, testRelID, testGroupEP)
	if err := st.ReplaceRelease(ctx(), testGroupID, bad); err == nil {
		t.Error("mismatched group = nil, want error")
	}
	// Missing group (no release to replace).
	if err := st.ReplaceRelease(ctx(), testGroupEP, bad); !apperrors.Is(err, apperrors.ErrNotFound) {
		t.Errorf("missing group err = %v, want NOT_FOUND", err)
	}
}

func TestDeleteReleaseGroup(t *testing.T) {
	st := newTestStore(t)
	_ = st.CreateArtist(ctx(), Artist{ID: testArtistID, Name: "Test Artist"})
	if err := st.SaveDiscography(ctx(), []FullReleaseParams{testRelease(testTrackID, testRelID, testGroupID)}); err != nil {
		t.Fatalf("SaveDiscography = %v", err)
	}
	if err := st.DeleteReleaseGroup(ctx(), testGroupID, false); err != nil {
		t.Fatalf("DeleteReleaseGroup = %v", err)
	}
	if _, err := st.GetReleaseGroup(ctx(), testGroupID); !apperrors.Is(err, apperrors.ErrNotFound) {
		t.Errorf("group should be gone: %v", err)
	}
	if err := st.DeleteReleaseGroup(ctx(), testGroupID, false); !apperrors.Is(err, apperrors.ErrNotFound) {
		t.Errorf("second delete = %v, want NOT_FOUND", err)
	}
}

func TestListReleaseGroups(t *testing.T) {
	st := newTestStore(t)
	_ = st.CreateArtist(ctx(), Artist{ID: testArtistID, Name: "Test Artist"})
	_ = st.CreateArtist(ctx(), Artist{ID: testArtistB, Name: "Other"})
	rel := testRelease(testTrackID, testRelID, testGroupID)
	if err := st.SaveDiscography(ctx(), []FullReleaseParams{rel}); err != nil {
		t.Fatalf("SaveDiscography = %v", err)
	}
	ep := testRelease(testTrackID2, testRelID2, testGroupEP)
	ep.Group.Title = "EP Group"
	ep.Group.Type = "ep"
	ep.Title = "EP Release"
	if err := st.SaveDiscography(ctx(), []FullReleaseParams{ep}); err != nil {
		t.Fatalf("SaveDiscography ep = %v", err)
	}

	groups, total, err := st.ListReleaseGroups(ctx(), ReleaseGroupListOptions{PageOptions: PageOptions{Limit: 20}})
	if err != nil {
		t.Fatalf("ListReleaseGroups = %v", err)
	}
	if total != 2 {
		t.Fatalf("total = %d, want 2", total)
	}
	_ = groups

	// Type filter.
	_, total, _ = st.ListReleaseGroups(ctx(), ReleaseGroupListOptions{
		PageOptions: PageOptions{Limit: 20}, Type: "ep",
	})
	if total != 1 {
		t.Errorf("ep total = %d, want 1", total)
	}

	// Artist filter.
	_, total, _ = st.ListReleaseGroups(ctx(), ReleaseGroupListOptions{
		PageOptions: PageOptions{Limit: 20}, ArtistID: testArtistID,
	})
	if total != 2 {
		t.Errorf("artist total = %d, want 2", total)
	}

	// HasRelease=true (all have releases here).
	has := true
	_, total, _ = st.ListReleaseGroups(ctx(), ReleaseGroupListOptions{
		PageOptions: PageOptions{Limit: 20}, HasRelease: &has,
	})
	if total != 2 {
		t.Errorf("hasRelease total = %d, want 2", total)
	}

	// IncludeRelease attaches summary.
	groups, _, _ = st.ListReleaseGroups(ctx(), ReleaseGroupListOptions{
		PageOptions: PageOptions{Limit: 20}, IncludeRelease: true,
	})
	for _, g := range groups {
		if g.Release == nil {
			t.Errorf("group %q missing release summary", g.ID)
		}
	}

	// Query filter.
	_, total, _ = st.ListReleaseGroups(ctx(), ReleaseGroupListOptions{
		SearchOptions: SearchOptions{Query: "EP Group"},
		PageOptions:   PageOptions{Limit: 20},
	})
	if total != 1 {
		t.Errorf("query total = %d, want 1", total)
	}
}

func TestGetTrack(t *testing.T) {
	st := newTestStore(t)
	_ = st.CreateArtist(ctx(), Artist{ID: testArtistID, Name: "Test Artist"})
	if err := st.SaveDiscography(ctx(), []FullReleaseParams{testRelease(testTrackID, testRelID, testGroupID)}); err != nil {
		t.Fatalf("SaveDiscography = %v", err)
	}
	got, err := st.GetTrack(ctx(), testTrackID)
	if err != nil {
		t.Fatalf("GetTrack = %v", err)
	}
	if got.Title != "Test Track" {
		t.Errorf("title = %q", got.Title)
	}
	if _, err := st.GetTrack(ctx(), "00000000-0000-4000-8000-000000000000"); !apperrors.Is(err, apperrors.ErrNotFound) {
		t.Errorf("missing track err = %v, want NOT_FOUND", err)
	}

	rel, track, err := st.GetTrackFullRelease(ctx(), testTrackID)
	if err != nil {
		t.Fatalf("GetTrackFullRelease = %v", err)
	}
	if rel.ID != testRelID || track.ID != testTrackID {
		t.Errorf("got rel=%q track=%q", rel.ID, track.ID)
	}
	if _, _, err := st.GetTrackFullRelease(ctx(), "00000000-0000-4000-8000-000000000000"); !apperrors.Is(err, apperrors.ErrNotFound) {
		t.Errorf("missing full release err = %v, want NOT_FOUND", err)
	}
}

func TestSaveDiscographyPreservesUserSelectedRelease(t *testing.T) {
	st := newTestStore(t)
	_ = st.CreateArtist(ctx(), Artist{ID: testArtistID, Name: "Test Artist"})
	if err := st.SaveDiscography(ctx(), []FullReleaseParams{testRelease(testTrackID, testRelID, testGroupID)}); err != nil {
		t.Fatalf("SaveDiscography A = %v", err)
	}

	replacement := testRelease(testTrackID2, testRelID2, testGroupID)
	replacement.Title = "Replacement"
	if err := st.ReplaceRelease(ctx(), testGroupID, replacement); err != nil {
		t.Fatalf("ReplaceRelease = %v", err)
	}

	// A later refresh brings back the originally preferred release. It must
	// succeed without overwriting the user's selection.
	refreshed := testRelease(testTrackID, testRelID, testGroupID)
	refreshed.Group.Title = "Updated Group Title"
	if err := st.SaveDiscography(ctx(), []FullReleaseParams{refreshed}); err != nil {
		t.Fatalf("SaveDiscography refresh = %v", err)
	}

	got, err := st.GetReleaseOfGroup(ctx(), testGroupID, false)
	if err != nil {
		t.Fatalf("GetReleaseOfGroup = %v", err)
	}
	if got.ID != testRelID2 {
		t.Errorf("stored release = %q, want %q (user selection preserved)", got.ID, testRelID2)
	}
	if _, err := st.GetRelease(ctx(), testRelID, false); !apperrors.Is(err, apperrors.ErrNotFound) {
		t.Errorf("original release should stay skipped, got %v", err)
	}
	grp, err := st.GetReleaseGroup(ctx(), testGroupID)
	if err != nil {
		t.Fatalf("GetReleaseGroup = %v", err)
	}
	if grp.Title != "Updated Group Title" {
		t.Errorf("group title = %q, want updated title (group still refreshes)", grp.Title)
	}
}

func TestSaveFullReleaseReconcilesChangedTracklist(t *testing.T) {
	st := newTestStore(t)
	_ = st.CreateArtist(ctx(), Artist{ID: testArtistID, Name: "Test Artist"})

	const (
		recA   = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
		recB   = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
		recC   = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
		track3 = "99999999-0000-4000-8000-000000000001"
	)

	mkTrack := func(id string, pos int, rec string) TrackParams {
		return TrackParams{
			ID: id, ReleaseID: testRelID, Disc: 1, Position: pos,
			Number: "1", Title: "Track", Credit: testCredit(testArtistID, "Test Artist"),
			RecordingID: rec,
		}
	}
	initial := testRelease(testTrackID, testRelID, testGroupID)
	initial.Tracks = []TrackParams{
		mkTrack(testTrackID, 1, recA),
		mkTrack(testTrackID2, 2, recB),
	}
	if err := st.SaveDiscography(ctx(), []FullReleaseParams{initial}); err != nil {
		t.Fatalf("SaveDiscography initial = %v", err)
	}

	root := t.TempDir()
	stFiles := newTestStore(t, root)
	_ = stFiles.CreateArtist(ctx(), Artist{ID: testArtistID, Name: "Test Artist"})
	if err := stFiles.SaveDiscography(ctx(), []FullReleaseParams{initial}); err != nil {
		t.Fatalf("SaveDiscography files initial = %v", err)
	}
	rootID := testRootID(t, stFiles)
	f1 := insertFile(t, stFiles, rootID, root+"/one.mp3", strptr(testTrackID))
	f2 := insertFile(t, stFiles, rootID, root+"/two.mp3", strptr(testTrackID2))

	// Changed tracklist: T1 moves 1->2 (conflicts with the old T2 slot),
	// T2 is removed, T3 is inserted at 1. The old delete-then-upsert order
	// would hit UNIQUE(release_id, disc, position) without parking.
	updated := testRelease(testTrackID, testRelID, testGroupID)
	updated.Tracks = []TrackParams{
		mkTrack(track3, 1, recC),
		mkTrack(testTrackID, 2, recA),
	}
	if err := st.SaveDiscography(ctx(), []FullReleaseParams{updated}); err != nil {
		t.Fatalf("SaveDiscography changed = %v (must not abort)", err)
	}
	got, err := st.GetRelease(ctx(), testRelID, false)
	if err != nil {
		t.Fatalf("GetRelease = %v", err)
	}
	if len(got.Tracks) != 2 {
		t.Fatalf("tracks = %d, want 2", len(got.Tracks))
	}
	byID := map[string]TrackParams{}
	for _, tr := range got.Tracks {
		byID[tr.ID] = tr
	}
	if tr, ok := byID[testTrackID]; !ok || tr.Position != 2 {
		t.Errorf("retained track pos = %+v, want pos 2", tr)
	}
	if tr, ok := byID[track3]; !ok || tr.Position != 1 {
		t.Errorf("new track = %+v, want pos 1", tr)
	}
	if _, ok := byID[testTrackID2]; ok {
		t.Error("removed track still present, want it deleted")
	}

	// Same reconciliation on the store with linked files: the retained
	// track keeps its link, the removed track's file unlinks.
	if err := stFiles.SaveDiscography(ctx(), []FullReleaseParams{updated}); err != nil {
		t.Fatalf("SaveDiscography files changed = %v", err)
	}
	gf1, err := stFiles.GetFile(ctx(), f1, false)
	if err != nil {
		t.Fatalf("GetFile f1 = %v", err)
	}
	if gf1.TrackID == nil || *gf1.TrackID != testTrackID {
		t.Errorf("f1 track = %v, want %q (retained link survives)", gf1.TrackID, testTrackID)
	}
	gf2, err := stFiles.GetFile(ctx(), f2, false)
	if err != nil {
		t.Fatalf("GetFile f2 = %v", err)
	}
	if gf2.TrackID != nil {
		t.Errorf("f2 track = %q, want unlinked (track removed)", *gf2.TrackID)
	}
}

func TestSaveFullReleaseSkipsDuplicatePositions(t *testing.T) {
	st := newTestStore(t)
	_ = st.CreateArtist(ctx(), Artist{ID: testArtistID, Name: "Test Artist"})

	const (
		recA = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
		recB = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	)
	rel := testRelease(testTrackID, testRelID, testGroupID)
	rel.Tracks = []TrackParams{
		{ID: testTrackID, ReleaseID: testRelID, Disc: 1, Position: 1,
			Number: "1", Title: "First", Credit: testCredit(testArtistID, "Test Artist"), RecordingID: recA},
		{ID: testTrackID2, ReleaseID: testRelID, Disc: 1, Position: 1,
			Number: "1", Title: "Duplicate Position", Credit: testCredit(testArtistID, "Test Artist"), RecordingID: recB},
	}
	if err := st.SaveDiscography(ctx(), []FullReleaseParams{rel}); err != nil {
		t.Fatalf("SaveDiscography dup positions = %v (must skip, not abort)", err)
	}
	got, err := st.GetRelease(ctx(), testRelID, false)
	if err != nil {
		t.Fatalf("GetRelease = %v", err)
	}
	if len(got.Tracks) != 1 || got.Tracks[0].ID != testTrackID {
		t.Errorf("tracks = %+v, want only the first at pos 1", got.Tracks)
	}
}

func TestReplaceReleaseRemapsLinksByRecording(t *testing.T) {
	root := t.TempDir()
	st := newTestStore(t, root)
	_ = st.CreateArtist(ctx(), Artist{ID: testArtistID, Name: "Test Artist"})
	rootID := testRootID(t, st)

	const (
		recShared = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
		recOld    = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
		recNew    = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
		oldTrack2 = "66666666-7777-4888-b999-aaaaaaaaaaaa"
		newTrack1 = "99999999-0000-4000-8000-000000000001"
		newTrack2 = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	)

	mkTrack := func(id, relID string, pos int, rec string) TrackParams {
		return TrackParams{
			ID: id, ReleaseID: relID, Disc: 1, Position: pos,
			Number: "1", Title: "Track", Credit: testCredit(testArtistID, "Test Artist"),
			RecordingID: rec,
		}
	}
	oldRel := testRelease(testTrackID, testRelID, testGroupID)
	oldRel.Tracks = []TrackParams{
		mkTrack(testTrackID, testRelID, 1, recShared),
		mkTrack(oldTrack2, testRelID, 2, recOld),
	}
	if err := st.SaveDiscography(ctx(), []FullReleaseParams{oldRel}); err != nil {
		t.Fatalf("SaveDiscography old = %v", err)
	}
	f1 := insertFile(t, st, rootID, root+"/one.mp3", strptr(testTrackID))
	f2 := insertFile(t, st, rootID, root+"/two.mp3", strptr(oldTrack2))

	newRel := testRelease(newTrack1, testRelID2, testGroupID)
	newRel.Title = "Replacement"
	newRel.Tracks = []TrackParams{
		mkTrack(newTrack1, testRelID2, 1, recShared),
		mkTrack(newTrack2, testRelID2, 2, recNew),
	}
	if err := st.ReplaceRelease(ctx(), testGroupID, newRel); err != nil {
		t.Fatalf("ReplaceRelease = %v", err)
	}

	gf1, err := st.GetFile(ctx(), f1, false)
	if err != nil {
		t.Fatalf("GetFile f1 = %v", err)
	}
	if gf1.TrackID == nil || *gf1.TrackID != newTrack1 {
		t.Errorf("f1 track = %v, want %q (recording+pos match remapped)", gf1.TrackID, newTrack1)
	}
	gf2, err := st.GetFile(ctx(), f2, false)
	if err != nil {
		t.Fatalf("GetFile f2 = %v", err)
	}
	if gf2.TrackID != nil {
		t.Errorf("f2 track = %q, want unlinked (no recording+pos match)", *gf2.TrackID)
	}
	if _, err := st.GetRelease(ctx(), testRelID, false); !apperrors.Is(err, apperrors.ErrNotFound) {
		t.Errorf("old release should be gone, got %v", err)
	}
}
