package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	apperrors "github.com/ppablomunoz/coda/internal/errors"
)

// Artist cover sources. 'upload' records manual uploads, which reuse the
// fetcher layout (same disk layout, same table) via tasks.SaveArtistCover.
const (
	ArtistCoverSourceDeezer    = "deezer"
	ArtistCoverSourceWikimedia = "wikimedia"
	ArtistCoverSourceUpload    = "upload"
)

// ArtistCoverParams is the metadata row for a cached artist photo. Bytes live
// on disk at config.ArtistCoverPath(...); this row is the truth for what is
// cached. Artist art is display-only: it is never embedded into audio files.
// Removed marks a user-deleted photo (tombstone): getters return the row
// but callers treat it as missing without refetching. Never serialized.
type ArtistCoverParams struct {
	ArtistID  string  `json:"artist_id"`
	Source    string  `json:"source"`
	SourceURL *string `json:"source_url"`
	MIME      string  `json:"mime"`
	SizeBytes int64   `json:"size_bytes"`
	FetchedAt string  `json:"fetched_at"`
	Removed   bool    `json:"-"`
}

// UpsertArtistCover inserts or replaces the cover row for an artist.
// fetched_at is always refreshed to now (millisecond precision, so two
// rapid updates never share an ETag) and any removal tombstone is cleared;
// the caller writes the image bytes to config.ArtistCoverPath separately.
// A recorded miss is cleared in the same transaction.
func (st *Store) UpsertArtistCover(ctx context.Context, p ArtistCoverParams) error {
	tx, err := st.db.BeginTx(ctx, nil)
	if err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("upsert artist cover start tx: %w", err),
		)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO artist_covers
			(artist_id, source, source_url, mime, size_bytes, fetched_at, removed)
		VALUES (?, ?, ?, ?, ?, strftime('%Y-%m-%dT%H:%M:%fZ','now'), 0)
		ON CONFLICT(artist_id) DO UPDATE SET
			source=excluded.source,
			source_url=excluded.source_url,
			mime=excluded.mime,
			size_bytes=excluded.size_bytes,
			fetched_at=strftime('%Y-%m-%dT%H:%M:%fZ','now'),
			removed=0`,
		p.ArtistID, p.Source, p.SourceURL, p.MIME, p.SizeBytes,
	); err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("upsert artist cover: %w", err),
		)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM artist_cover_misses WHERE artist_id = ?`, p.ArtistID,
	); err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("clear artist cover miss: %w", err),
		)
	}
	if err := tx.Commit(); err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("upsert artist cover commit: %w", err),
		)
	}
	return nil
}

// GetArtistCover returns the cover row for an artist, or ErrNotFound when the
// artist has no cached photo. A user-removed row is returned with Removed
// set; callers must not fetch or serve it. Callers combine a live row with
// config.ArtistCoverPath and treat a missing file as a cache miss
// (re-fetch or 404).
func (st *Store) GetArtistCover(ctx context.Context, artistID string) (*ArtistCoverParams, error) {
	var c ArtistCoverParams
	var sourceURL sql.NullString
	var removed int
	if err := st.db.QueryRowContext(ctx, `
		SELECT artist_id, source, source_url, mime, size_bytes, fetched_at, removed
		FROM artist_covers
		WHERE artist_id = ?`, artistID,
	).Scan(&c.ArtistID, &c.Source, &sourceURL, &c.MIME, &c.SizeBytes, &c.FetchedAt, &removed); err != nil {
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
	c.Removed = removed != 0
	return &c, nil
}

// MarkArtistCoverRemoved records a user-deleted photo without deleting the
// row, so later GETs stay 404 instead of lazy-fetching provider art again.
// Idempotent. The placeholder source/mime satisfy the NOT NULL row shape;
// Removed keeps the row from ever being served. Any recorded miss is cleared
// in the same transaction: the tombstone alone suppresses fetches.
func (st *Store) MarkArtistCoverRemoved(ctx context.Context, artistID string) error {
	tx, err := st.db.BeginTx(ctx, nil)
	if err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("remove artist cover start tx: %w", err),
		)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO artist_covers
			(artist_id, source, source_url, mime, size_bytes, fetched_at, removed)
		VALUES (?, 'upload', NULL, 'image/jpeg', 0, strftime('%Y-%m-%dT%H:%M:%fZ','now'), 1)
		ON CONFLICT(artist_id) DO UPDATE SET
			removed=1,
			fetched_at=strftime('%Y-%m-%dT%H:%M:%fZ','now')`,
		artistID,
	); err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("remove artist cover: %w", err),
		)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM artist_cover_misses WHERE artist_id = ?`, artistID,
	); err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("clear artist cover miss: %w", err),
		)
	}
	if err := tx.Commit(); err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("remove artist cover commit: %w", err),
		)
	}
	return nil
}

// DeleteArtistCover removes the cover row for an artist. It is idempotent:
// deleting an artist with no cached photo is a no-op. File removal happens
// in the caller. Any recorded miss is cleared too so the next GET retries
// providers.
func (st *Store) DeleteArtistCover(ctx context.Context, artistID string) error {
	tx, err := st.db.BeginTx(ctx, nil)
	if err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("delete artist cover start tx: %w", err),
		)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM artist_covers WHERE artist_id = ?`, artistID,
	); err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("delete artist cover: %w", err),
		)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM artist_cover_misses WHERE artist_id = ?`, artistID,
	); err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("clear artist cover miss: %w", err),
		)
	}
	if err := tx.Commit(); err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("delete artist cover commit: %w", err),
		)
	}
	return nil
}
