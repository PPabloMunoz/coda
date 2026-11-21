package tags

import (
	"reflect"
	"testing"

	"go.senan.xyz/taglib"
)

func TestParsePair(t *testing.T) {
	cases := []struct {
		in        string
		wantN     int
		wantTotal int
	}{
		{"", 0, 0},
		{"3", 3, 0},
		{"3/12", 3, 12},
		{" 03 / 12 ", 3, 12},
		{"garbage", 0, 0},
		{"x/y", 0, 0},
		{"5/", 5, 0},
	}
	for _, tc := range cases {
		n, total := parsePair(tc.in)
		if n != tc.wantN || total != tc.wantTotal {
			t.Errorf("parsePair(%q) = (%d,%d), want (%d,%d)", tc.in, n, total, tc.wantN, tc.wantTotal)
		}
	}
}

func TestIsTruthyTag(t *testing.T) {
	for _, s := range []string{"1", "true", "TRUE", "yes", "Yes"} {
		if !isTruthy(s) {
			t.Errorf("isTruthy(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"", "0", "false", "no", "maybe"} {
		if isTruthy(s) {
			t.Errorf("isTruthy(%q) = true, want false", s)
		}
	}
}

func TestFromMapArtistsNeverSplit(t *testing.T) {
	raw := map[string][]string{
		taglib.Title:  {"Song"},
		taglib.Artist: {"A & B"},
		// No ARTISTS key on purpose.
	}
	s := fromMap(raw)
	if s.ArtistRaw != "A & B" {
		t.Errorf("ArtistRaw = %q, want %q", s.ArtistRaw, "A & B")
	}
	if len(s.Artists) != 0 {
		t.Errorf("Artists = %v, want empty (never split ARTIST)", s.Artists)
	}
}

func TestFromMapMultiValue(t *testing.T) {
	raw := map[string][]string{
		taglib.Title:               {"Song"},
		taglib.Artists:             {"Alice", "Bob"},
		taglib.MusicBrainzArtistID: {"id-1", "id-2"},
		taglib.TrackNumber:         {"3/12"},
		taglib.DiscNumber:          {"2"},
		taglib.Compilation:         {"true"},
		taglib.MusicBrainzAlbumID:  {"album-id"},
		taglib.ReleaseType:         {"Album"},
	}
	s := fromMap(raw)
	if !reflect.DeepEqual(s.Artists, []string{"Alice", "Bob"}) {
		t.Errorf("Artists = %v", s.Artists)
	}
	if !reflect.DeepEqual(s.MBArtistIDs, []string{"id-1", "id-2"}) {
		t.Errorf("MBArtistIDs = %v", s.MBArtistIDs)
	}
	if s.TrackNumber != 3 || s.TrackTotal != 12 {
		t.Errorf("track = %d/%d, want 3/12", s.TrackNumber, s.TrackTotal)
	}
	if s.DiscNumber != 2 {
		t.Errorf("disc = %d, want 2", s.DiscNumber)
	}
	if !s.Compilation {
		t.Error("Compilation = false, want true")
	}
	if s.ReleaseType != "Album" {
		t.Errorf("ReleaseType = %q", s.ReleaseType)
	}
}

func TestFromMapNil(t *testing.T) {
	s := fromMap(nil)
	if s == nil {
		t.Fatal("fromMap(nil) = nil, want empty snapshot")
	}
	if s.Title != "" || len(s.Artists) != 0 {
		t.Errorf("got %+v, want empty", s)
	}
}

func TestAlbumArtistsPrefersList(t *testing.T) {
	raw := map[string][]string{
		taglib.AlbumArtist: {"Display"},
		albumArtistsKey:    {"Alice", "Bob"},
	}
	s := fromMap(raw)
	if !reflect.DeepEqual(s.AlbumArtists, []string{"Alice", "Bob"}) {
		t.Errorf("AlbumArtists = %v, want list", s.AlbumArtists)
	}

	raw = map[string][]string{taglib.AlbumArtist: {"Solo"}}
	s = fromMap(raw)
	if !reflect.DeepEqual(s.AlbumArtists, []string{"Solo"}) {
		t.Errorf("AlbumArtists fallback = %v, want [Solo]", s.AlbumArtists)
	}
}

func TestLinkKeysStable(t *testing.T) {
	keys := LinkKeys()
	if len(keys) == 0 {
		t.Fatal("LinkKeys() empty")
	}
	seen := map[string]bool{}
	for _, k := range keys {
		if seen[k] {
			t.Errorf("duplicate key %q", k)
		}
		seen[k] = true
	}
	for _, want := range []string{taglib.Title, taglib.Artist, taglib.MusicBrainzTrackID} {
		if !seen[want] {
			t.Errorf("LinkKeys missing %q", want)
		}
	}
}

func TestToMapRoundTrip(t *testing.T) {
	s := &Snapshot{
		Title: "Song", Album: "Record", ArtistRaw: "Alice",
		Artists:     []string{"Alice", "Bob"},
		AlbumArtist: "Alice", AlbumArtists: []string{"Alice"},
		TrackNumber: 3, DiscNumber: 1, Date: "2024",
		MBTrackID: "track-id", MBAlbumID: "album-id",
		MBReleaseGroupID: "rg-id", MBReleaseTrackID: "rt-id",
		MBArtistIDs:      []string{"a1"},
		MBAlbumArtistIDs: []string{"a1"},
		ReleaseType:      "Album", ReleaseStatus: "Official",
	}
	m := ToMap(s)
	back := fromMap(m)
	if back.Title != s.Title || back.Album != s.Album || back.ArtistRaw != s.ArtistRaw {
		t.Errorf("round trip display = %+v, want %+v", back, s)
	}
	if !reflect.DeepEqual(back.Artists, s.Artists) {
		t.Errorf("Artists = %v, want %v", back.Artists, s.Artists)
	}
	if back.TrackNumber != 3 || back.DiscNumber != 1 {
		t.Errorf("numbers = %d/%d, want 3/1", back.TrackNumber, back.DiscNumber)
	}
	if back.MBTrackID != s.MBTrackID || back.MBAlbumID != s.MBAlbumID {
		t.Errorf("MBIDs = %+v, want %+v", back, s)
	}
}

func TestToMapOmitsEmpty(t *testing.T) {
	m := ToMap(&Snapshot{})
	if len(m) != 0 {
		t.Errorf("ToMap(empty) = %v, want empty", m)
	}
}

func TestSnapshotFromLink(t *testing.T) {
	info := &LinkInfo{
		Title: "T", Album: "A", ArtistDisplay: "Alice & Bob",
		Artists: []string{"Alice", "Bob"}, AlbumArtistDisplay: "Alice",
		AlbumArtists: []string{"Alice"}, TrackNumber: 4, DiscNumber: 2,
		Date: "2023", MBTrackID: "t", MBAlbumID: "a",
		ReleaseType: "Single", ReleaseStatus: "Official",
	}
	s := SnapshotFromLink(info)
	if s.Title != "T" || s.ArtistRaw != "Alice & Bob" || s.TrackNumber != 4 || s.DiscNumber != 2 {
		t.Errorf("got %+v", s)
	}
	if !reflect.DeepEqual(s.Artists, info.Artists) {
		t.Errorf("Artists = %v", s.Artists)
	}
}

func TestSplitTXXXValues(t *testing.T) {
	got := splitTXXXValues("a\x00\x00b\x00c")
	if !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Errorf("got %q", got)
	}
	if out := splitTXXXValues(""); len(out) != 0 {
		t.Errorf("empty = %q, want empty", out)
	}
}
