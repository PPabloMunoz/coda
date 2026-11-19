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
