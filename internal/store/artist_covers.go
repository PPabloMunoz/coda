package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	apperrors "github.com/ppablomunoz/coda/internal/errors"
)

// Artist cover sources. 'upload' is reserved for a future manual-upload path
// so no schema change is needed when it lands (same disk layout, same table).
const (
	ArtistCoverSourceDeezer    = "deezer"
	ArtistCoverSourceWikimedia = "wikimedia"
	ArtistCoverSourceUpload    = "upload"
)

// ArtistCoverParams is the metadata row for a cached artist photo. Bytes live
// on disk at config.ArtistCoverPath(...); this row is the truth for what is
// cached. Artist art is display-only: it is never embedded into audio files.
type ArtistCoverParams struct {
	ArtistID  string  `json:"artist_id"`
	Source    string  `json:"source"`
	SourceURL *string `json:"source_url"`
	MIME      string  `json:"mime"`
	SizeBytes int64   `json:"size_bytes"`
	FetchedAt string  `json:"fetched_at"`
}

// UpsertArtistCover inserts or replaces the cover row for an artist.
// fetched_at is always refreshed to now; the caller writes the image bytes
// to config.ArtistCoverPath separately.
func (st *Store) UpsertArtistCover(ctx context.Context, p ArtistCoverParams) error {
	if _, err := st.db.ExecContext(ctx, `
		INSERT INTO artist_covers
			(artist_id, source, source_url, mime, size_bytes, fetched_at)
		VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(artist_id) DO UPDATE SET
			source=excluded.source,
			source_url=excluded.source_url,
			mime=excluded.mime,
			size_bytes=excluded.size_bytes,
			fetched_at=CURRENT_TIMESTAMP`,
		p.ArtistID, p.Source, p.SourceURL, p.MIME, p.SizeBytes,
	); err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("upsert artist cover: %w", err),
		)
	}
	return nil
}

// GetArtistCover returns the cover row for an artist, or ErrNotFound when the
// artist has no cached photo. Callers combine it with config.ArtistCoverPath
// and treat a missing file as a cache miss (re-fetch or 404).
func (st *Store) GetArtistCover(ctx context.Context, artistID string) (*ArtistCoverParams, error) {
	var c ArtistCoverParams
	var sourceURL sql.NullString
	if err := st.db.QueryRowContext(ctx, `
		SELECT artist_id, source, source_url, mime, size_bytes, fetched_at
		FROM artist_covers
		WHERE artist_id = ?`, artistID,
	).Scan(&c.ArtistID, &c.Source, &sourceURL, &c.MIME, &c.SizeBytes, &c.FetchedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, apperrors.ErrNotFound
		}
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("query artist cover: %w", err),
		)
	}
	if sourceURL.Valid {
		c.SourceURL = &sourceURL.String
	}
	return &c, nil
}

// DeleteArtistCover removes the cover row for an artist. It is idempotent:
// deleting an artist with no cached photo is a no-op. File removal happens
// in the caller.
func (st *Store) DeleteArtistCover(ctx context.Context, artistID string) error {
	if _, err := st.db.ExecContext(ctx,
		`DELETE FROM artist_covers WHERE artist_id = ?`, artistID,
	); err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("delete artist cover: %w", err),
		)
	}
	return nil
}
