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
type CoverParams struct {
	ReleaseID string  `json:"release_id"`
	Source    string  `json:"source"`
	SourceURL *string `json:"source_url"`
	MIME      string  `json:"mime"`
	SizeBytes int64   `json:"size_bytes"`
	FetchedAt string  `json:"fetched_at"`
}

// UpsertCover inserts or replaces the cover row for a release. fetched_at is
// always refreshed to now; the caller writes the image bytes to
// config.CoverPath separately.
func (st *Store) UpsertCover(ctx context.Context, p CoverParams) error {
	if _, err := st.db.ExecContext(ctx, `
		INSERT INTO release_covers
			(release_id, source, source_url, mime, size_bytes, fetched_at)
		VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(release_id) DO UPDATE SET
			source=excluded.source,
			source_url=excluded.source_url,
			mime=excluded.mime,
			size_bytes=excluded.size_bytes,
			fetched_at=CURRENT_TIMESTAMP`,
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
// release has no cached art. Callers combine it with config.CoverPath and
// treat a missing file as a cache miss (re-fetch or 404).
func (st *Store) GetCover(ctx context.Context, releaseID string) (*CoverParams, error) {
	var c CoverParams
	var sourceURL sql.NullString
	if err := st.db.QueryRowContext(ctx, `
		SELECT release_id, source, source_url, mime, size_bytes, fetched_at
		FROM release_covers
		WHERE release_id = ?`, releaseID,
	).Scan(&c.ReleaseID, &c.Source, &sourceURL, &c.MIME, &c.SizeBytes, &c.FetchedAt); err != nil {
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
	return &c, nil
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
