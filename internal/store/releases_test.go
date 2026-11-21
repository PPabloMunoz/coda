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
	if err := st.DeleteReleaseGroup(ctx(), testGroupID); err != nil {
		t.Fatalf("DeleteReleaseGroup = %v", err)
	}
	if _, err := st.GetReleaseGroup(ctx(), testGroupID); !apperrors.Is(err, apperrors.ErrNotFound) {
		t.Errorf("group should be gone: %v", err)
	}
	if err := st.DeleteReleaseGroup(ctx(), testGroupID); !apperrors.Is(err, apperrors.ErrNotFound) {
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
