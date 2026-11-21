package store

import (
	"path/filepath"
	"testing"

	apperrors "github.com/ppablomunoz/coda/internal/errors"
)

func TestAddRootDirs(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	st := newStoreAt(t, dbPath, root)
	if got := st.RootPaths(); len(got) != 1 || got[0] != root {
		t.Fatalf("RootPaths = %v, want [%q]", got, root)
	}
	var active int
	if err := st.db.QueryRow(`SELECT active FROM roots WHERE path = ?`, root).Scan(&active); err != nil {
		t.Fatalf("query root = %v", err)
	}
	if active != 1 {
		t.Errorf("active = %d, want 1", active)
	}

	// Reopen same DB with no roots: row stays but is deactivated.
	st2 := newStoreAt(t, dbPath)
	var active2 int
	if err := st2.db.QueryRow(`SELECT active FROM roots WHERE path = ?`, root).Scan(&active2); err != nil {
		t.Fatalf("query root after empty = %v", err)
	}
	if active2 != 0 {
		t.Errorf("active after empty = %d, want 0", active2)
	}
}

func TestAddRootDirsReplaces(t *testing.T) {
	rootA := t.TempDir()
	rootB := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	_ = newStoreAt(t, dbPath, rootA)
	st2 := newStoreAt(t, dbPath, rootB)

	var n int
	if err := st2.db.QueryRow(`SELECT COUNT(*) FROM roots WHERE path = ?`, rootA).Scan(&n); err != nil {
		t.Fatalf("count = %v", err)
	}
	if n != 0 {
		t.Errorf("old root still present, want deleted")
	}
	if err := st2.db.QueryRow(`SELECT COUNT(*) FROM roots WHERE path = ?`, rootB).Scan(&n); err != nil {
		t.Fatalf("count = %v", err)
	}
	if n != 1 {
		t.Errorf("new root missing")
	}
}

func TestCreateAndGetArtist(t *testing.T) {
	st := newTestStore(t)
	if err := st.CreateArtist(ctx(), Artist{ID: testArtistID, Name: "Test Artist"}); err != nil {
		t.Fatalf("CreateArtist = %v", err)
	}
	got, err := st.GetArtistByID(ctx(), testArtistID)
	if err != nil {
		t.Fatalf("GetArtistByID = %v", err)
	}
	if got.Name != "Test Artist" || got.Active != 1 || got.InLibrary != 1 {
		t.Errorf("got %+v", got)
	}

	if _, err := st.GetArtistByID(ctx(), "00000000-0000-4000-8000-000000000000"); !apperrors.Is(err, apperrors.ErrNotFound) {
		t.Errorf("missing artist err = %v, want NOT_FOUND", err)
	}
}

func TestListArtistsFiltering(t *testing.T) {
	st := newTestStore(t)
	_ = st.CreateArtist(ctx(), Artist{ID: testArtistID, Name: "The Beatles"})
	_ = st.CreateArtist(ctx(), Artist{ID: testArtistB, Name: "Pink Floyd"})

	artists, total, err := st.ListArtists(ctx(), ArtistListOptions{PageOptions: PageOptions{Limit: 20}})
	if err != nil {
		t.Fatalf("ListArtists = %v", err)
	}
	if total != 2 || len(artists) != 2 {
		t.Fatalf("total = %d len = %d, want 2", total, len(artists))
	}

	// Query filter (case-insensitive).
	artists, total, err = st.ListArtists(ctx(), ArtistListOptions{
		SearchOptions: SearchOptions{Query: "beatles"},
		PageOptions:   PageOptions{Limit: 20},
	})
	if err != nil {
		t.Fatalf("ListArtists query = %v", err)
	}
	if total != 1 || artists[0].Name != "The Beatles" {
		t.Errorf("query got total=%d %+v", total, artists)
	}

	// Diacritics-insensitive via unaccent.
	_ = st.CreateArtist(ctx(), Artist{ID: "bbbbbbbb-cccc-4ddd-aeee-ffffffffffff", Name: "Beyoncé"})
	_, total, err = st.ListArtists(ctx(), ArtistListOptions{
		SearchOptions: SearchOptions{Query: "beyonce"},
		PageOptions:   PageOptions{Limit: 20},
	})
	if err != nil {
		t.Fatalf("ListArtists unaccent = %v", err)
	}
	if total != 1 {
		t.Errorf("unaccent query total = %d, want 1", total)
	}
}

func TestDeleteArtist(t *testing.T) {
	st := newTestStore(t)
	_ = st.CreateArtist(ctx(), Artist{ID: testArtistID, Name: "Test Artist"})
	if err := st.SaveDiscography(ctx(), []FullReleaseParams{testRelease(testTrackID, testRelID, testGroupID)}); err != nil {
		t.Fatalf("SaveDiscography = %v", err)
	}

	pruned, err := st.DeleteArtist(ctx(), testArtistID)
	if err != nil {
		t.Fatalf("DeleteArtist = %v", err)
	}
	_ = pruned

	if _, err := st.GetArtistByID(ctx(), testArtistID); !apperrors.Is(err, apperrors.ErrNotFound) {
		t.Errorf("deleted artist visible: %v", err)
	}
	if _, err := st.GetRelease(ctx(), testRelID, false); !apperrors.Is(err, apperrors.ErrNotFound) {
		t.Errorf("release should cascade-delete: %v", err)
	}

	if _, err := st.DeleteArtist(ctx(), testArtistID); !apperrors.Is(err, apperrors.ErrNotFound) {
		t.Errorf("second delete = %v, want NOT_FOUND", err)
	}
	if _, err := st.DeleteArtist(ctx(), "00000000-0000-4000-8000-000000000000"); !apperrors.Is(err, apperrors.ErrNotFound) {
		t.Errorf("missing delete = %v, want NOT_FOUND", err)
	}
}

func TestPruneOrphanCollaborators(t *testing.T) {
	st := newTestStore(t)
	// Library artist + collaborator-only artist sharing a release.
	_ = st.CreateArtist(ctx(), Artist{ID: testArtistID, Name: "Main"})
	p := testRelease(testTrackID, testRelID, testGroupID)
	p.Group.Credit = []CreditEntry{
		{ArtistID: testArtistID, Name: "Main", CreditedName: "Main"},
		{ArtistID: testArtistB, Name: "Guest", CreditedName: "Guest", JoinPhrase: " feat. "},
	}
	p.Credit = p.Group.Credit
	p.Tracks[0].Credit = p.Group.Credit
	if err := st.SaveDiscography(ctx(), []FullReleaseParams{p}); err != nil {
		t.Fatalf("SaveDiscography = %v", err)
	}

	// Collaborator is in_library=0; pruning with tracks present keeps them.
	if n, err := st.PruneOrphanCollaborators(ctx()); err != nil || n != 0 {
		t.Errorf("prune with tracks = %d, %v; want 0, nil", n, err)
	}

	// Delete the library artist cascades the release; guest becomes trackless.
	if _, err := st.DeleteArtist(ctx(), testArtistID); err != nil {
		t.Fatalf("DeleteArtist = %v", err)
	}
}
