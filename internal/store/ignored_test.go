package store

import (
	"testing"

	apperrors "github.com/ppablomunoz/coda/internal/errors"
)

func TestPermanentReleaseGroupDeleteSurvivesRefresh(t *testing.T) {
	st := newTestStore(t)
	_ = st.CreateArtist(ctx(), Artist{ID: testArtistID, Name: "Test Artist"})
	rel := testRelease(testTrackID, testRelID, testGroupID)
	ep := testRelease(testTrackID2, testRelID2, testGroupEP)
	ep.Group.Title = "EP Group"
	ep.Group.Type = "ep"
	ep.Title = "EP Release"
	if err := st.SaveDiscography(ctx(), []FullReleaseParams{rel, ep}); err != nil {
		t.Fatalf("SaveDiscography = %v", err)
	}

	if err := st.DeleteReleaseGroup(ctx(), testGroupID, true); err != nil {
		t.Fatalf("DeleteReleaseGroup permanent = %v", err)
	}

	items, total, err := st.ListIgnored(ctx(), IgnoredListOptions{PageOptions: PageOptions{Limit: 20}})
	if err != nil {
		t.Fatalf("ListIgnored = %v", err)
	}
	if total != 1 || items[0].ID != testGroupID || items[0].Type != "release_group" {
		t.Fatalf("ignored = %+v total=%d, want the deleted group", items, total)
	}

	// A later refresh brings back both groups; the ignored one must stay gone.
	if err := st.SaveDiscography(ctx(), []FullReleaseParams{rel, ep}); err != nil {
		t.Fatalf("SaveDiscography refresh = %v", err)
	}
	if _, err := st.GetReleaseGroup(ctx(), testGroupID); !apperrors.Is(err, apperrors.ErrNotFound) {
		t.Errorf("ignored group resurrected: %v", err)
	}
	got, err := st.GetReleaseGroup(ctx(), testGroupEP)
	if err != nil {
		t.Errorf("sibling group should survive: %v", err)
	} else if got.Title != "EP Group" {
		t.Errorf("sibling title = %q", got.Title)
	}

	// Removing the tombstone lets the next refresh recreate it.
	if err := st.RemoveIgnored(ctx(), testGroupID); err != nil {
		t.Fatalf("RemoveIgnored = %v", err)
	}
	if err := st.SaveDiscography(ctx(), []FullReleaseParams{rel}); err != nil {
		t.Fatalf("SaveDiscography after restore = %v", err)
	}
	if _, err := st.GetReleaseGroup(ctx(), testGroupID); err != nil {
		t.Errorf("restored group should import again: %v", err)
	}
}

func TestRemoveIgnoredReactivatesArtistAsCollaborator(t *testing.T) {
	st := newTestStore(t)
	_ = st.CreateArtist(ctx(), Artist{ID: testArtistID, Name: "Main"})
	p := testRelease(testTrackID, testRelID, testGroupID)
	p.Group.Credit = []CreditEntry{
		{ArtistID: testArtistID, Name: "Main", CreditedName: "Main"},
		{ArtistID: testArtistB, Name: "Guest", CreditedName: "Guest"},
	}
	p.Credit = p.Group.Credit
	p.Tracks[0].Credit = p.Group.Credit
	if err := st.SaveDiscography(ctx(), []FullReleaseParams{p}); err != nil {
		t.Fatalf("SaveDiscography = %v", err)
	}

	// Permanently delete the guest: the shared group survives because the
	// library artist is still credited.
	if _, err := st.DeleteArtist(ctx(), testArtistB, true); err != nil {
		t.Fatalf("DeleteArtist guest permanent = %v", err)
	}
	if _, err := st.GetArtistByID(ctx(), testArtistB); !apperrors.Is(err, apperrors.ErrNotFound) {
		t.Fatalf("deleted guest GetArtistByID = %v, want NOT_FOUND", err)
	}

	// Allowing again reactivates the row as collaboration-only: the artist
	// page can offer Add to library instead of 404ing.
	if err := st.RemoveIgnored(ctx(), testArtistB); err != nil {
		t.Fatalf("RemoveIgnored = %v", err)
	}
	got, err := st.GetArtistByID(ctx(), testArtistB)
	if err != nil {
		t.Fatalf("GetArtistByID after restore = %v", err)
	}
	if got.Active != 1 || got.InLibrary != 0 {
		t.Errorf("restored guest = active %d in_library %d, want 1/0", got.Active, got.InLibrary)
	}
}

func TestNormalReleaseGroupDeleteDoesNotIgnore(t *testing.T) {
	st := newTestStore(t)
	_ = st.CreateArtist(ctx(), Artist{ID: testArtistID, Name: "Test Artist"})
	if err := st.SaveDiscography(ctx(), []FullReleaseParams{testRelease(testTrackID, testRelID, testGroupID)}); err != nil {
		t.Fatalf("SaveDiscography = %v", err)
	}
	if err := st.DeleteReleaseGroup(ctx(), testGroupID, false); err != nil {
		t.Fatalf("DeleteReleaseGroup = %v", err)
	}
	_, total, err := st.ListIgnored(ctx(), IgnoredListOptions{PageOptions: PageOptions{Limit: 20}})
	if err != nil {
		t.Fatalf("ListIgnored = %v", err)
	}
	if total != 0 {
		t.Errorf("normal delete tombstoned total=%d, want 0", total)
	}
	if err := st.SaveDiscography(ctx(), []FullReleaseParams{testRelease(testTrackID, testRelID, testGroupID)}); err != nil {
		t.Fatalf("SaveDiscography refresh = %v", err)
	}
	if _, err := st.GetReleaseGroup(ctx(), testGroupID); err != nil {
		t.Errorf("normal delete should allow re-import: %v", err)
	}
}

func TestPermanentArtistDeleteBlocksReadd(t *testing.T) {
	st := newTestStore(t)
	_ = st.CreateArtist(ctx(), Artist{ID: testArtistID, Name: "Test Artist"})
	if err := st.SaveDiscography(ctx(), []FullReleaseParams{testRelease(testTrackID, testRelID, testGroupID)}); err != nil {
		t.Fatalf("SaveDiscography = %v", err)
	}
	if _, err := st.DeleteArtist(ctx(), testArtistID, true); err != nil {
		t.Fatalf("DeleteArtist permanent = %v", err)
	}
	if err := st.CreateArtist(ctx(), Artist{ID: testArtistID, Name: "Test Artist"}); !apperrors.Is(err, apperrors.ErrForbidden) {
		t.Errorf("re-add ignored artist = %v, want FORBIDDEN", err)
	}
	if err := st.SaveArtistDiscography(ctx(), testArtistID, []FullReleaseParams{testRelease(testTrackID, testRelID, testGroupID)}); !apperrors.Is(err, apperrors.ErrNotFound) {
		t.Errorf("import ignored artist = %v, want NOT_FOUND (canceled)", err)
	}
	if err := st.RemoveIgnored(ctx(), testArtistID); err != nil {
		t.Fatalf("RemoveIgnored = %v", err)
	}
	if err := st.CreateArtist(ctx(), Artist{ID: testArtistID, Name: "Test Artist"}); err != nil {
		t.Fatalf("re-add after restore = %v", err)
	}
	if err := st.SaveArtistDiscography(ctx(), testArtistID, []FullReleaseParams{testRelease(testTrackID, testRelID, testGroupID)}); err != nil {
		t.Fatalf("import after restore = %v", err)
	}
}

func TestSaveArtistDiscographySkipsRemovedArtist(t *testing.T) {
	st := newTestStore(t)
	_ = st.CreateArtist(ctx(), Artist{ID: testArtistID, Name: "Test Artist"})
	if err := st.SaveArtistDiscography(ctx(), testArtistID, []FullReleaseParams{testRelease(testTrackID, testRelID, testGroupID)}); err != nil {
		t.Fatalf("initial import = %v", err)
	}
	if _, err := st.DeleteArtist(ctx(), testArtistID, false); err != nil {
		t.Fatalf("DeleteArtist = %v", err)
	}
	if err := st.SaveArtistDiscography(ctx(), testArtistID, []FullReleaseParams{testRelease(testTrackID, testRelID, testGroupID)}); !apperrors.Is(err, apperrors.ErrNotFound) {
		t.Errorf("import after delete = %v, want NOT_FOUND (canceled)", err)
	}
	if _, err := st.GetReleaseGroup(ctx(), testGroupID); !apperrors.Is(err, apperrors.ErrNotFound) {
		t.Errorf("deleted artist group resurrected: %v", err)
	}
}

func TestIgnoredCollaboratorStaysInactive(t *testing.T) {
	st := newTestStore(t)
	_ = st.CreateArtist(ctx(), Artist{ID: testArtistID, Name: "Main"})
	p := testRelease(testTrackID, testRelID, testGroupID)
	p.Group.Credit = []CreditEntry{
		{ArtistID: testArtistID, Name: "Main", CreditedName: "Main"},
		{ArtistID: testArtistB, Name: "Guest", CreditedName: "Guest"},
	}
	p.Credit = p.Group.Credit
	p.Tracks[0].Credit = p.Group.Credit
	if err := st.SaveDiscography(ctx(), []FullReleaseParams{p}); err != nil {
		t.Fatalf("SaveDiscography = %v", err)
	}
	// Permanently delete the guest (it is a collaborator row after import).
	// Promote it first so DeleteArtist finds an active row.
	if _, err := st.db.Exec(`UPDATE artists SET active = 1 WHERE id = ?`, testArtistB); err != nil {
		t.Fatalf("promote guest = %v", err)
	}
	if _, err := st.DeleteArtist(ctx(), testArtistB, true); err != nil {
		t.Fatalf("DeleteArtist guest permanent = %v", err)
	}
	if err := st.SaveDiscography(ctx(), []FullReleaseParams{p}); err != nil {
		t.Fatalf("SaveDiscography refresh = %v", err)
	}
	var active int
	if err := st.db.QueryRow(`SELECT active FROM artists WHERE id = ?`, testArtistB).Scan(&active); err != nil {
		t.Fatalf("query guest = %v", err)
	}
	if active != 0 {
		t.Errorf("ignored collaborator active = %d, want 0", active)
	}
}
