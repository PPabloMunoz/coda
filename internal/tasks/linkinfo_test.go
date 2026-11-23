package tasks

import (
	"testing"

	"github.com/ppablomunoz/coda/internal/store"
)

func strPtr(s string) *string { return &s }

func TestCreditDisplay(t *testing.T) {
	display, names, ids := creditDisplay([]store.CreditEntry{
		{ArtistID: "a1", Name: "Alice", CreditedName: "Alice", JoinPhrase: " & "},
		{ArtistID: "b2", Name: "Bob", CreditedName: "", JoinPhrase: ""},
	})
	if display != "Alice & Bob" {
		t.Errorf("display = %q", display)
	}
	if len(names) != 2 || names[0] != "Alice" || names[1] != "Bob" {
		t.Errorf("names = %v (blank credited falls back to Name)", names)
	}
	if len(ids) != 2 || ids[0] != "a1" {
		t.Errorf("ids = %v", ids)
	}

	display, names, ids = creditDisplay(nil)
	if display != "" || len(names) != 0 || len(ids) != 0 {
		t.Errorf("nil = %q %v %v", display, names, ids)
	}
}

func TestLinkInfoFromStore(t *testing.T) {
	rel := &store.FullReleaseParams{
		ID: "rel-1", Title: "Album", Date: strPtr("2024"), Status: strPtr("official"),
		Credit: []store.CreditEntry{{ArtistID: "a1", Name: "Album Artist", CreditedName: "Album Artist"}},
		Group: store.ReleaseGroupParams{
			ID: "rg-1", Title: "Group", Type: "album",
			Credit: []store.CreditEntry{{ArtistID: "a1", Name: "Album Artist", CreditedName: "Album Artist"}},
		},
	}
	track := &store.TrackParams{
		ID: "track-1", Title: "Song", Disc: 0, Position: 3,
		RecordingID: "rec-1",
		Credit:      []store.CreditEntry{{ArtistID: "t1", Name: "Track Artist", CreditedName: "Track Artist"}},
	}
	got := LinkInfoFromStore(rel, track)
	if got.Title != "Song" || got.Album != "Album" {
		t.Errorf("got %+v", got)
	}
	if got.ArtistDisplay != "Track Artist" || len(got.Artists) != 1 {
		t.Errorf("track credit = %q %v", got.ArtistDisplay, got.Artists)
	}
	if got.AlbumArtistDisplay != "Album Artist" {
		t.Errorf("album artist = %q", got.AlbumArtistDisplay)
	}
	if got.TrackNumber != 3 || got.DiscNumber != 1 {
		t.Errorf("numbers = %d/%d, want 3/1 (disc clamps to 1)", got.TrackNumber, got.DiscNumber)
	}
	if got.DiscTotal != 1 {
		t.Errorf("DiscTotal = %d, want 1 (no track rows = single-disc)", got.DiscTotal)
	}
	if got.MBReleaseTrackID != "track-1" || got.MBTrackID != "rec-1" {
		t.Errorf("mbids = %+v", got)
	}

	// Track with no credit falls back to release credit.
	bare := &store.TrackParams{ID: "t2", Title: "Bare", Position: 1, Disc: 1}
	got = LinkInfoFromStore(rel, bare)
	if got.ArtistDisplay != "Album Artist" {
		t.Errorf("fallback artist = %q, want release credit", got.ArtistDisplay)
	}

	// Release with no credit falls back to group credit.
	relNoCredit := &store.FullReleaseParams{
		ID: "rel-2", Title: "Album2",
		Group: store.ReleaseGroupParams{
			ID: "rg-2", Title: "G2", Type: "single",
			Credit: []store.CreditEntry{{ArtistID: "g1", Name: "Group Artist", CreditedName: "Group Artist"}},
		},
	}
	got = LinkInfoFromStore(relNoCredit, bare)
	if got.AlbumArtistDisplay != "Group Artist" {
		t.Errorf("album fallback = %q", got.AlbumArtistDisplay)
	}

	// Multi-disc release reports the highest disc number.
	multi := &store.FullReleaseParams{
		ID: "rel-3", Title: "Anthology",
		Group: store.ReleaseGroupParams{ID: "rg-3", Title: "G3", Type: "album"},
		Tracks: []store.TrackParams{
			{ID: "m1", Disc: 1, Position: 1, Title: "A"},
			{ID: "m2", Disc: 3, Position: 1, Title: "B"},
		},
	}
	got = LinkInfoFromStore(multi, &store.TrackParams{ID: "m1", Title: "A", Position: 1, Disc: 1})
	if got.DiscTotal != 3 {
		t.Errorf("DiscTotal = %d, want 3 (max disc across tracks)", got.DiscTotal)
	}
}

func TestFolderArtistFromGroup(t *testing.T) {
	rel := &store.FullReleaseParams{
		ID: "rel-1", Title: "Album",
		Credit: []store.CreditEntry{{ArtistID: "r1", Name: "Release Artist", CreditedName: "Release Artist"}},
		Group: store.ReleaseGroupParams{
			ID: "rg-1", Title: "Group", Type: "album",
			Credit: []store.CreditEntry{
				{ArtistID: "g1", Name: "Group First", CreditedName: "Group First"},
				{ArtistID: "g2", Name: "Group Second", CreditedName: "Group Second", JoinPhrase: ""},
			},
		},
	}
	track := &store.TrackParams{ID: "t1", Title: "Song", Position: 1, Disc: 1}
	got := LinkInfoFromStore(rel, track)
	if got.FolderArtist != "Group First" {
		t.Errorf("FolderArtist = %q, want first group artist", got.FolderArtist)
	}
	if got.AlbumArtistDisplay != "Release Artist" {
		t.Errorf("AlbumArtistDisplay = %q, want full release display (tags unchanged)", got.AlbumArtistDisplay)
	}

	// No group credit: falls back to release, then track credit.
	bare := &store.FullReleaseParams{
		ID: "rel-2", Title: "Album2",
		Credit: []store.CreditEntry{{ArtistID: "r2", Name: "Release Only"}},
		Group:  store.ReleaseGroupParams{ID: "rg-2", Title: "G2", Type: "single"},
	}
	if got := LinkInfoFromStore(bare, track); got.FolderArtist != "Release Only" {
		t.Errorf("FolderArtist = %q, want release fallback", got.FolderArtist)
	}
	empty := &store.FullReleaseParams{
		ID: "rel-3", Title: "Album3",
		Group: store.ReleaseGroupParams{ID: "rg-3", Title: "G3", Type: "single"},
	}
	credited := &store.TrackParams{
		ID: "t2", Title: "Song", Position: 1, Disc: 1,
		Credit: []store.CreditEntry{{ArtistID: "t1", Name: "Plain", CreditedName: "As Credited"}},
	}
	if got := LinkInfoFromStore(empty, credited); got.FolderArtist != "As Credited" {
		t.Errorf("FolderArtist = %q, want credited track fallback", got.FolderArtist)
	}
	if got := LinkInfoFromStore(empty, track); got.FolderArtist != "" {
		t.Errorf("FolderArtist = %q, want empty when nothing credited", got.FolderArtist)
	}
}

func TestDeref(t *testing.T) {
	if deref(nil) != "" {
		t.Error("deref(nil) != empty")
	}
	if deref(strPtr("x")) != "x" {
		t.Error("deref(x) != x")
	}
}
