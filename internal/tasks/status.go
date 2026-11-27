package tasks

import "time"

type ArtistImportState string

const (
	ArtistImportIdle    ArtistImportState = "idle"
	ArtistImportQueued  ArtistImportState = "queued"
	ArtistImportRunning ArtistImportState = "running"
	ArtistImportDone    ArtistImportState = "done"
	ArtistImportFailed  ArtistImportState = "failed"
)

type ArtistImportStatus struct {
	ArtistID   string            `json:"artist_id"`
	State      ArtistImportState `json:"state"`
	QueuedAt   *time.Time        `json:"queued_at,omitempty"`
	StartedAt  *time.Time        `json:"started_at,omitempty"`
	FinishedAt *time.Time        `json:"finished_at,omitempty"`
	Error      string            `json:"error,omitempty"`
}

// ScanState is the lifecycle of the library scan. It mirrors the artist
// import states: idle before the first scan, queued while waiting on the
// serial queue, running during the walk/sync/auto-link pass, and done or
// failed once finished.
type ScanState string

const (
	ScanIdle    ScanState = "idle"
	ScanQueued  ScanState = "queued"
	ScanRunning ScanState = "running"
	ScanDone    ScanState = "done"
	ScanFailed  ScanState = "failed"
)

// MaxScanFailedFiles bounds how many failed paths a scan status carries.
// The full count is always in FailedCount; FailedFiles keeps the first N
// paths so the status stays small.
const MaxScanFailedFiles = 50

// ScanStatus is the observable scan lifecycle plus the last run's
// diagnostics. Only terminal states (done/failed) persist across restarts;
// queued/running live in memory. Timestamps describe the current lifecycle;
// counters describe the last completed run (zero when none has completed),
// preserved while a new run is queued/running until it finishes.
type ScanStatus struct {
	State           ScanState  `json:"state"`
	QueuedAt        *time.Time `json:"queued_at,omitempty"`
	StartedAt       *time.Time `json:"started_at,omitempty"`
	FinishedAt      *time.Time `json:"finished_at,omitempty"`
	DurationMs      int64      `json:"duration_ms,omitempty"`
	Found           int        `json:"found"`
	Upserted        int        `json:"upserted"`
	Pruned          int64      `json:"pruned"`
	AutoLinked      int        `json:"autolinked"`
	Moved           int        `json:"moved"`
	SkippedConflict int        `json:"skipped_conflict"`
	FailedCount     int        `json:"failed_count"`
	FailedFiles     []string   `json:"failed_files,omitempty"`
	KeptRoots       []string   `json:"kept_roots,omitempty"`
	IncompleteRoots []string   `json:"incomplete_roots,omitempty"`
	Error           string     `json:"error,omitempty"`
}
