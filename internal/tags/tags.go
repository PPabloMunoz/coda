package tags

import (
	"fmt"
	"strconv"
	"strings"

	"go.senan.xyz/taglib"
)

// albumArtistsKey is the per-artist album credit list. TagLib has a
// constant for the singular ALBUMARTIST display value, but not for the
// multi-value ALBUMARTISTS tag used by Picard and Navidrome.
const albumArtistsKey = "ALBUMARTISTS"

// Snapshot is the read-only view of a file's tags needed to group by
// release (album/EP/single) and to match against MusicBrainz later.
// Single strings are display-only; ordered slices preserve multi-artist
// credits and IDs. 0 means a number tag was missing/unparseable.
// Raw keeps the full taglib map so future matching never re-reads files.
type Snapshot struct {
	Title     string
	Album     string
	ArtistRaw string // first ARTIST, display only, never split
	Artists   []string

	AlbumArtist  string
	AlbumArtists []string

	TrackNumber  int
	TrackTotal   int
	DiscNumber   int
	DiscTotal    int
	DiscSubtitle string

	Date         string
	OriginalDate string
	Compilation  bool

	Genres []string

	TitleSort       string
	AlbumSort       string
	ArtistSort      string
	AlbumArtistSort string

	MBTrackID        string
	MBAlbumID        string
	MBReleaseGroupID string
	MBReleaseTrackID string
	MBWorkID         string
	MBArtistIDs      []string
	MBAlbumArtistIDs []string

	ISRC          string
	Barcode       string
	CatalogNumber string
	Label         string
	ASIN          string

	ReleaseType    string
	ReleaseStatus  string
	ReleaseCountry string
}

// Read is the package-level convenience over TaglibReader.
func Read(path string) (*Snapshot, error) {
	raw, err := taglib.ReadTags(path)
	if err != nil {
		return nil, fmt.Errorf("tags: read %q: %w", path, err)
	}
	return fromMap(raw), nil
}

// fromMap builds a Snapshot from an already-read taglib map.
// ARTISTS comes only from ARTISTS, never by splitting ARTIST, so a
// single "A & B" string stays ambiguous instead of becoming false data.
func fromMap(raw map[string][]string) *Snapshot {
	if raw == nil {
		raw = map[string][]string{}
	}

	track, trackTotal := parsePair(first(raw, taglib.TrackNumber))
	disc, discTotal := parsePair(first(raw, taglib.DiscNumber))

	s := &Snapshot{
		Title:     first(raw, taglib.Title),
		Album:     first(raw, taglib.Album),
		ArtistRaw: first(raw, taglib.Artist),
		Artists:   copyAll(raw[taglib.Artists]),

		AlbumArtist:  first(raw, taglib.AlbumArtist),
		AlbumArtists: albumArtists(raw),

		TrackNumber:  track,
		TrackTotal:   trackTotal,
		DiscNumber:   disc,
		DiscTotal:    discTotal,
		DiscSubtitle: first(raw, taglib.DiscSubtitle),

		Date:         first(raw, taglib.Date),
		OriginalDate: first(raw, taglib.OriginalDate),
		Compilation:  isTruthy(first(raw, taglib.Compilation)),

		Genres: copyAll(raw[taglib.Genre]),

		TitleSort:       first(raw, taglib.TitleSort),
		AlbumSort:       first(raw, taglib.AlbumSort),
		ArtistSort:      first(raw, taglib.ArtistSort),
		AlbumArtistSort: first(raw, taglib.AlbumArtistSort),

		MBTrackID:        first(raw, taglib.MusicBrainzTrackID),
		MBAlbumID:        first(raw, taglib.MusicBrainzAlbumID),
		MBReleaseGroupID: first(raw, taglib.MusicBrainzReleaseGroupID),
		MBReleaseTrackID: first(raw, taglib.MusicBrainzReleaseTrackID),
		MBWorkID:         first(raw, taglib.MusicBrainzWorkID),
		MBArtistIDs:      copyAll(raw[taglib.MusicBrainzArtistID]),
		MBAlbumArtistIDs: copyAll(raw[taglib.MusicBrainzAlbumArtistID]),

		ISRC:          first(raw, taglib.ISRC),
		Barcode:       first(raw, taglib.Barcode),
		CatalogNumber: first(raw, taglib.CatalogNumber),
		Label:         first(raw, taglib.Label),
		ASIN:          first(raw, taglib.ASIN),

		ReleaseType:    first(raw, taglib.ReleaseType),
		ReleaseStatus:  first(raw, taglib.ReleaseStatus),
		ReleaseCountry: first(raw, taglib.ReleaseCountry),
	}

	return s
}

// first returns the first value for key, trimmed. Empty when absent.
func first(raw map[string][]string, key string) string {
	vals := raw[key]
	if len(vals) == 0 {
		return ""
	}
	return strings.TrimSpace(vals[0])
}

// copyAll preserves ordering for multi-valued tags (artists, IDs, genres).
func copyAll(vals []string) []string {
	if len(vals) == 0 {
		return nil
	}
	out := make([]string, len(vals))
	copy(out, vals)
	return out
}

// albumArtists prefers the per-artist list, falling back to the singular
// display tag for files tagged by tools that only write ALBUMARTIST.
func albumArtists(raw map[string][]string) []string {
	if vals := raw[albumArtistsKey]; len(vals) > 0 {
		return copyAll(vals)
	}
	return copyAll(raw[taglib.AlbumArtist])
}

// parsePair handles "3", "3/12", and " 03 / 12 ". Garbage yields 0, 0
// so callers treat it as missing rather than failing the whole file.
func parsePair(s string) (int, int) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, 0
	}
	head, tail, _ := strings.Cut(s, "/")
	n, _ := strconv.Atoi(strings.TrimSpace(head))
	if tail == "" {
		return n, 0
	}
	total, _ := strconv.Atoi(strings.TrimSpace(tail))
	return n, total
}

// isTruthy covers the spellings taggers use for the COMPILATION flag.
func isTruthy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}
