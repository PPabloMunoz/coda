package store

type Root struct {
	ID        int64  `json:"id"`
	Path      string `json:"path"`
	Active    int    `json:"active"`
	CreatedAt string `json:"created_at"`
}

type Artist struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	SortName       *string           `json:"sort-name"`
	Disambiguation *string           `json:"disambiguation"`
	Active         int               `json:"active"`
	InLibrary      int               `json:"in_library"`
	Stats          *ArtistTrackStats `json:"stats,omitempty"`
}

type ArtistTrackStats struct {
	TrackCount       int64 `json:"track_count"`
	LinkedTrackCount int64 `json:"linked_track_count"`
}

type ReleaseGroupParams struct {
	ID      string          `json:"id"`
	Title   string          `json:"title"`
	Type    string          `json:"type"` // album/ep/single/other
	Credit  []CreditEntry   `json:"credit"`
	Release *ReleaseSummary `json:"release,omitempty"`
}

// ReleaseSummary is the compact release view used when release groups are
// requested with ?include=release. It avoids a second request per group.
type ReleaseSummary struct {
	ID              string  `json:"id"`
	Title           string  `json:"title"`
	Date            *string `json:"date"`
	Status          *string `json:"status"`
	TrackCount      int64   `json:"track_count"`
	LinkedFileCount int64   `json:"linked_file_count"`
}

type CreditEntry struct {
	ArtistID       string  `json:"artist_id"`
	Name           string  `json:"name"`
	SortName       *string `json:"sort_name"`
	Disambiguation *string `json:"disambiguation"`
	CreditedName   string  `json:"credited_name"`
	JoinPhrase     string  `json:"join_phrase"`
}

type TrackParams struct {
	ID          string        `json:"id"`
	ReleaseID   string        `json:"release_id"`
	Disc        int           `json:"disc"`
	Position    int           `json:"position"`
	Number      string        `json:"number"`
	Title       string        `json:"title"`
	Credit      []CreditEntry `json:"credit"`
	DurMS       *int          `json:"dur_ms"`
	RecordingID string        `json:"recording_id"`
	Files       []LinkedFile  `json:"files,omitempty"`
}

// LinkedFile is the compact local-file view attached to a track when release
// responses are requested with ?include=files.
type LinkedFile struct {
	ID         int64  `json:"id"`
	Path       string `json:"path"`
	FormatHint string `json:"format_hint"`
	SizeBytes  int64  `json:"size_bytes"`
}

type FullReleaseParams struct {
	ID     string             `json:"id"`
	Title  string             `json:"title"`
	Date   *string            `json:"date"`
	Status *string            `json:"status"`
	Credit []CreditEntry      `json:"credit"`
	Group  ReleaseGroupParams `json:"group"`
	Tracks []TrackParams      `json:"tracks"`
}

type LocalFile struct {
	ID         int64        `json:"id"`
	RootID     int64        `json:"root_id"`
	Path       string       `json:"path"`
	SizeBytes  int64        `json:"size_bytes"`
	MtimeNs    int64        `json:"mtime_ns"`
	FormatHint string       `json:"format_hint"`
	ScannedAt  string       `json:"scanned_at"`
	TrackID    *string      `json:"track_id"`
	Track      *LinkedTrack `json:"track,omitempty"`
}

// LinkedTrack is the track context attached to a local file when file
// responses are requested with ?include=track.
type LinkedTrack struct {
	ID          string          `json:"id"`
	Title       string          `json:"title"`
	Disc        int             `json:"disc"`
	Position    int             `json:"position"`
	Number      string          `json:"number"`
	RecordingID string          `json:"recording_id"`
	Credit      []CreditEntry   `json:"credit"`
	Release     ReleaseRef      `json:"release"`
	Group       ReleaseGroupRef `json:"group"`
}

type ReleaseRef struct {
	ID     string  `json:"id"`
	Title  string  `json:"title"`
	Date   *string `json:"date"`
	Status *string `json:"status"`
}

type ReleaseGroupRef struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Type  string `json:"type"`
}

// IgnoredEntry is a permanently deleted artist or release group.
// The row blocks discography imports from recreating the entity until
// it is removed; restoring never auto-refreshes anything.
type IgnoredEntry struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Type      string `json:"type"` // artist | release_group
	DeletedAt string `json:"deleted_at"`
}
