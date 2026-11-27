package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	apperrors "github.com/ppablomunoz/coda/internal/errors"
)

// CoverMiss is a negative-cache entry: providers had no usable art at
// checked_at, and callers must not retry until now passes retry_after
// (unix seconds). Tombstones in release_covers/artist_covers are separate
// and always win over a miss.
type CoverMiss struct {
	ID         string
	Misses     int
	CheckedAt  string
	RetryAfter int64
}

// Active reports whether the miss still suppresses provider fetches.
func (m *CoverMiss) Active(now time.Time) bool {
	if m == nil {
		return false
	}
	return now.Unix() < m.RetryAfter
}

// GetReleaseCoverMiss returns the miss row for a release, or ErrNotFound
// when no miss was recorded (or it was cleared by an upload/refresh).
func (st *Store) GetReleaseCoverMiss(ctx context.Context, releaseID string) (*CoverMiss, error) {
	var m CoverMiss
	var checkedAt string
	if err := st.db.QueryRowContext(ctx, `
		SELECT release_id, checked_at, misses, retry_after
		FROM release_cover_misses
		WHERE release_id = ?`, releaseID,
	).Scan(&m.ID, &checkedAt, &m.Misses, &m.RetryAfter); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, apperrors.ErrNotFound
		}
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("query release cover miss: %w", err),
		)
	}
	m.CheckedAt = checkedAt
	return &m, nil
}

// UpsertReleaseCoverMiss records a provider miss for a release with the
// given strike count and retry time. checkedAt/retryAfter are stored as
// UTC (checked_at text, retry_after unix seconds).
func (st *Store) UpsertReleaseCoverMiss(ctx context.Context, releaseID string, misses int, checkedAt, retryAfter time.Time) error {
	if misses < 1 {
		misses = 1
	}
	checked := checkedAt.UTC().Format("2006-01-02T15:04:05.000Z")
	if _, err := st.db.ExecContext(ctx, `
		INSERT INTO release_cover_misses (release_id, checked_at, misses, retry_after)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(release_id) DO UPDATE SET
			checked_at=excluded.checked_at,
			misses=excluded.misses,
			retry_after=excluded.retry_after`,
		releaseID, checked, misses, retryAfter.UTC().Unix(),
	); err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("upsert release cover miss: %w", err),
		)
	}
	return nil
}

// DeleteReleaseCoverMiss clears the miss row for a release. It is
// idempotent: clearing a release with no miss is a no-op.
func (st *Store) DeleteReleaseCoverMiss(ctx context.Context, releaseID string) error {
	if _, err := st.db.ExecContext(ctx,
		`DELETE FROM release_cover_misses WHERE release_id = ?`, releaseID,
	); err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("delete release cover miss: %w", err),
		)
	}
	return nil
}

// GetArtistCoverMiss returns the miss row for an artist, or ErrNotFound
// when no miss was recorded.
func (st *Store) GetArtistCoverMiss(ctx context.Context, artistID string) (*CoverMiss, error) {
	var m CoverMiss
	var checkedAt string
	if err := st.db.QueryRowContext(ctx, `
		SELECT artist_id, checked_at, misses, retry_after
		FROM artist_cover_misses
		WHERE artist_id = ?`, artistID,
	).Scan(&m.ID, &checkedAt, &m.Misses, &m.RetryAfter); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, apperrors.ErrNotFound
		}
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("query artist cover miss: %w", err),
		)
	}
	m.CheckedAt = checkedAt
	return &m, nil
}

// UpsertArtistCoverMiss records a provider miss for an artist.
func (st *Store) UpsertArtistCoverMiss(ctx context.Context, artistID string, misses int, checkedAt, retryAfter time.Time) error {
	if misses < 1 {
		misses = 1
	}
	checked := checkedAt.UTC().Format("2006-01-02T15:04:05.000Z")
	if _, err := st.db.ExecContext(ctx, `
		INSERT INTO artist_cover_misses (artist_id, checked_at, misses, retry_after)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(artist_id) DO UPDATE SET
			checked_at=excluded.checked_at,
			misses=excluded.misses,
			retry_after=excluded.retry_after`,
		artistID, checked, misses, retryAfter.UTC().Unix(),
	); err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("upsert artist cover miss: %w", err),
		)
	}
	return nil
}

// DeleteArtistCoverMiss clears the miss row for an artist. Idempotent.
func (st *Store) DeleteArtistCoverMiss(ctx context.Context, artistID string) error {
	if _, err := st.db.ExecContext(ctx,
		`DELETE FROM artist_cover_misses WHERE artist_id = ?`, artistID,
	); err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("delete artist cover miss: %w", err),
		)
	}
	return nil
}
