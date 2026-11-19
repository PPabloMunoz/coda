package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

func (st *Store) GetTrack(ctx context.Context, trackID string) (*TrackParams, error) {
	stmt, err := st.db.PrepareContext(ctx, `
		SELECT t.id, t.release_id, t.disc_number, t.position, t.number,
		t.title, t.artist_credit_id, t.duration_ms, t.recording_id
		FROM tracks t
		WHERE id = ?`)
	if err != nil {
		return nil, st.prepareQueryError(err)
	}
	defer func() { _ = stmt.Close() }()

	var t TrackParams
	var acID sql.NullInt64

	if err := stmt.QueryRowContext(ctx, trackID).Scan(
		&t.ID,
		&t.ReleaseID,
		&t.Disc,
		&t.Position,
		&t.Number,
		&t.Title,
		&acID,
		&t.DurMS,
		&t.RecordingID,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("[store] query scan: %w", err)
	}

	if acID.Valid {
		credits, err := st.getCreditEntry(ctx, acID.Int64)
		if err != nil {
			return nil, err
		}
		t.Credit = credits
	}

	return &t, nil
}

func (st *Store) GetTrackFullRelease(ctx context.Context, trackID string) (
	*FullReleaseParams, *TrackParams, error,
) {
	track, err := st.GetTrack(ctx, trackID)
	if err != nil {
		return nil, nil, err
	}

	release, err := st.GetRelease(ctx, track.ReleaseID, false)
	if err != nil {
		return nil, nil, err
	}

	return release, track, nil
}

func (st *Store) trackExists(ctx context.Context, tx *sql.Tx, id string) (bool, error) {
	if id == "" {
		return false, nil
	}
	var exists bool
	err := tx.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM tracks WHERE id = ?)`, id,
	).Scan(&exists)
	if err != nil {
		return false, err
	}
	return exists, nil
}
