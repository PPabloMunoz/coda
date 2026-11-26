package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	apperrors "github.com/ppablomunoz/coda/internal/errors"
)

// Cover sources. 'upload' records manual uploads, which reuse the fetcher
// layout (same disk layout, same table) via tasks.SaveCover.
const (
	CoverSourceCoverArtArchive = "coverartarchive"
	CoverSourceDeezer          = "deezer"
	CoverSourceiTunes          = "itunes"
	CoverSourceUpload          = "upload"
)

// CoverParams is the metadata row for a cached release cover. Bytes live on
// disk at config.CoverPath(...); this row is the truth for what is cached.
// Removed marks a user-deleted cover (tombstone): getters return the row
// but callers treat it as missing without refetching. Never serialized.
type CoverParams struct {
	ReleaseID string  `json:"release_id"`
	Source    string  `json:"source"`
	SourceURL *string `json:"source_url"`
	MIME      string  `json:"mime"`
	SizeBytes int64   `json:"size_bytes"`
	FetchedAt string  `json:"fetched_at"`
	Removed   bool    `json:"-"`
}

// UpsertCover inserts or replaces the cover row for a release. fetched_at
// is always refreshed to now (millisecond precision, so two rapid updates
// never share an ETag) and any removal tombstone is cleared; the caller
// writes the image bytes to config.CoverPath separately.
func (st *Store) UpsertCover(ctx context.Context, p CoverParams) error {
	if _, err := st.db.ExecContext(ctx, `
		INSERT INTO release_covers
			(release_id, source, source_url, mime, size_bytes, fetched_at, removed)
		VALUES (?, ?, ?, ?, ?, strftime('%Y-%m-%dT%H:%M:%fZ','now'), 0)
		ON CONFLICT(release_id) DO UPDATE SET
			source=excluded.source,
			source_url=excluded.source_url,
			mime=excluded.mime,
			size_bytes=excluded.size_bytes,
			fetched_at=strftime('%Y-%m-%dT%H:%M:%fZ','now'),
			removed=0`,
		p.ReleaseID, p.Source, p.SourceURL, p.MIME, p.SizeBytes,
	); err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("upsert cover: %w", err),
		)
	}
	return nil
}

// GetCover returns the cover row for a release, or ErrNotFound when the
// release has no cached art. A user-removed row is returned with Removed
// set; callers must not fetch or serve it. Callers combine a live row with
// config.CoverPath and treat a missing file as a cache miss (re-fetch or
// 404).
func (st *Store) GetCover(ctx context.Context, releaseID string) (*CoverParams, error) {
	var c CoverParams
	var sourceURL sql.NullString
	var removed int
	if err := st.db.QueryRowContext(ctx, `
		SELECT release_id, source, source_url, mime, size_bytes, fetched_at, removed
		FROM release_covers
		WHERE release_id = ?`, releaseID,
	).Scan(&c.ReleaseID, &c.Source, &sourceURL, &c.MIME, &c.SizeBytes, &c.FetchedAt, &removed); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, apperrors.ErrNotFound
		}
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("query cover: %w", err),
		)
	}
	if sourceURL.Valid {
		c.SourceURL = &sourceURL.String
	}
	c.Removed = removed != 0
	return &c, nil
}

// MarkCoverRemoved records a user-deleted cover without deleting the row,
// so later GETs stay 404 instead of lazy-fetching provider art again.
// Idempotent: removing an already-removed or never-cached cover is a no-op.
// The placeholder source/mime satisfy the NOT NULL row shape; Removed
// keeps the row from ever being served or embedded.
func (st *Store) MarkCoverRemoved(ctx context.Context, releaseID string) error {
	if _, err := st.db.ExecContext(ctx, `
		INSERT INTO release_covers
			(release_id, source, source_url, mime, size_bytes, fetched_at, removed)
		VALUES (?, 'upload', NULL, 'image/jpeg', 0, strftime('%Y-%m-%dT%H:%M:%fZ','now'), 1)
		ON CONFLICT(release_id) DO UPDATE SET
			removed=1,
			fetched_at=strftime('%Y-%m-%dT%H:%M:%fZ','now')`,
		releaseID,
	); err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("remove cover: %w", err),
		)
	}
	return nil
}

// DeleteCover removes the cover row for a release. It is idempotent: deleting
// a release with no cached art is a no-op. File removal happens in the caller.
func (st *Store) DeleteCover(ctx context.Context, releaseID string) error {
	if _, err := st.db.ExecContext(ctx,
		`DELETE FROM release_covers WHERE release_id = ?`, releaseID,
	); err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("delete cover: %w", err),
		)
	}
	return nil
}
