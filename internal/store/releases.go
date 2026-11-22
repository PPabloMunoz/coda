package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	apperrors "github.com/ppablomunoz/coda/internal/errors"
)

// GetReleaseOfGroup returns the single release kept for a release-group.
// One release per group is enforced by the UNIQUE on releases.release_group_id.
func (st *Store) GetReleaseOfGroup(ctx context.Context,
	groupID string, includeFiles bool,
) (*FullReleaseParams, error) {
	var releaseID string
	if err := st.db.QueryRowContext(ctx,
		`SELECT id FROM releases WHERE release_group_id = ?`,
		groupID,
	).Scan(&releaseID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, apperrors.ErrNotFound
		}
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("query release by group: %w", err),
		)
	}

	return st.GetRelease(ctx, releaseID, includeFiles)
}

// GetRelease returns a release with group, track credits, and optional linked files.
func (st *Store) GetRelease(ctx context.Context,
	releaseID string, includeFiles bool,
) (*FullReleaseParams, error) {
	var r FullReleaseParams
	var rgID string
	var acID sql.NullInt64

	if err := st.db.QueryRowContext(ctx, `
		SELECT r.id, r.title, r.date, r.status, 
			r.release_group_id, r.artist_credit_id
		FROM releases r
		WHERE id = ?`, releaseID).Scan(
		&r.ID, &r.Title, &r.Date, &r.Status, &rgID, &acID,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, apperrors.ErrNotFound
		}
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("query release: %w", err),
		)
	}

	if acID.Valid {
		credits, err := st.getCreditEntry(ctx, acID.Int64)
		if err != nil {
			return nil, err
		}
		r.Credit = credits
	}

	group, err := st.GetReleaseGroup(ctx, rgID)
	if err != nil {
		return nil, err
	}
	r.Group = *group

	trackRows, err := st.db.QueryContext(ctx, `
		SELECT id, release_id, disc_number, position, number, title,
			artist_credit_id, duration_ms, recording_id
		FROM tracks
		WHERE release_id = ?
		ORDER BY disc_number, position`,
		releaseID,
	)
	if err != nil {
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("query tracks: %w", err),
		)
	}
	defer func() { _ = trackRows.Close() }()

	tracks := []TrackParams{}
	creditIDs := []int64{}
	seen := map[int64]struct{}{}
	for trackRows.Next() {
		var t TrackParams
		var creditID sql.NullInt64
		var durMS sql.NullInt64
		if err := trackRows.Scan(
			&t.ID, &t.ReleaseID, &t.Disc, &t.Position, &t.Number,
			&t.Title, &creditID, &durMS, &t.RecordingID,
		); err != nil {
			return nil, apperrors.Wrap(
				apperrors.ErrInternal,
				fmt.Errorf("scan track: %w", err),
			)
		}
		if durMS.Valid {
			v := int(durMS.Int64)
			t.DurMS = &v
		}
		if creditID.Valid {
			creditIDs = append(creditIDs, creditID.Int64)
			seen[creditID.Int64] = struct{}{}
		} else {
			creditIDs = append(creditIDs, -1)
		}
		tracks = append(tracks, t)
	}
	if err := trackRows.Err(); err != nil {
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("reading tracks: %w", err),
		)
	}

	if len(seen) > 0 {
		byCredit, err := st.getCreditEntries(ctx, seen)
		if err != nil {
			return nil, err
		}
		for i, cid := range creditIDs {
			if cid >= 0 {
				tracks[i].Credit = byCredit[cid]
			}
		}
	}
	if includeFiles {
		if err := st.attachTrackFiles(ctx, releaseID, tracks); err != nil {
			return nil, err
		}
	}
	r.Tracks = tracks

	return &r, nil
}

// attachTrackFiles adds the local files linked to each track in one query.
func (st *Store) attachTrackFiles(
	ctx context.Context,
	releaseID string,
	tracks []TrackParams,
) error {
	if len(tracks) == 0 {
		return nil
	}

	rows, err := st.db.QueryContext(ctx, `
		SELECT lf.id, lf.path, lf.format_hint, lf.size_bytes, lf.track_id
		FROM local_files lf
		JOIN tracks t ON t.id = lf.track_id
		WHERE t.release_id = ?
		ORDER BY lf.path`,
		releaseID,
	)
	if err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("query track files: %w", err),
		)
	}
	defer func() { _ = rows.Close() }()

	byTrack := make(map[string]int, len(tracks))
	for i := range tracks {
		byTrack[tracks[i].ID] = i
	}

	for rows.Next() {
		var file LinkedFile
		var trackID string
		if err := rows.Scan(
			&file.ID, &file.Path, &file.FormatHint, &file.SizeBytes, &trackID,
		); err != nil {
			return apperrors.Wrap(
				apperrors.ErrInternal,
				fmt.Errorf("scan track file: %w", err),
			)
		}

		if index, ok := byTrack[trackID]; ok {
			tracks[index].Files = append(tracks[index].Files, file)
		}
	}
	if err := rows.Err(); err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("reading track files: %w", err),
		)
	}

	return nil
}

// normalizeRGType maps MusicBrainz primary types to album/ep/single/other.
func normalizeRGType(t string) string {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "album":
		return "album"
	case "ep":
		return "ep"
	case "single":
		return "single"
	default:
		return "other"
	}
}

// SaveFullRelease upserts the group, its single release and tracks in one tx,
// keyed by MBID throughout. Tracks with missing position/title are skipped.
func (st *Store) SaveFullRelease(ctx context.Context, tx *sql.Tx, p FullReleaseParams) error {
	rgCreditID, err := createCreditTx(ctx, tx, p.Group.Credit)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO release_groups (id, title, type, artist_credit_id)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			title=excluded.title,
			type=excluded.type,
			artist_credit_id=excluded.artist_credit_id`,
		p.Group.ID, p.Group.Title, normalizeRGType(p.Group.Type), rgCreditID,
	); err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("upsert release-group: %w", err),
		)
	}

	relCreditID, err := createCreditTx(ctx, tx, p.Credit)
	if err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO releases
			(id, release_group_id, title, artist_credit_id, date, status)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			release_group_id=excluded.release_group_id,
			title=excluded.title,
			artist_credit_id=excluded.artist_credit_id,
			date=excluded.date,
			status=excluded.status`,
		p.ID, p.Group.ID, p.Title, relCreditID,
		p.Date, p.Status,
	); err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("upsert release: %w", err),
		)
	}

	if len(p.Tracks) == 0 {
		return nil
	}

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO tracks
			(id, release_id, disc_number, position, number, 
			title, artist_credit_id, duration_ms, recording_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			number=excluded.number,
			release_id=excluded.release_id,
			disc_number=excluded.disc_number,
			position=excluded.position,
			title=excluded.title,
			artist_credit_id=excluded.artist_credit_id,
			duration_ms=excluded.duration_ms,
			recording_id=excluded.recording_id`,
	)
	if err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("prepare query: %w", err),
		)
	}
	defer func() { _ = stmt.Close() }()

	for _, t := range p.Tracks {
		if t.Position < 1 || strings.TrimSpace(t.Title) == "" {
			continue
		}
		disc := max(t.Disc, 1)

		trackCreditID, err := createCreditTx(ctx, tx, t.Credit)
		if err != nil {
			return err
		}

		if _, err := stmt.ExecContext(ctx,
			t.ID, p.ID, disc, t.Position, t.Number,
			t.Title, trackCreditID, t.DurMS, t.RecordingID,
		); err != nil {
			return apperrors.Wrap(
				apperrors.ErrInternal,
				fmt.Errorf("upsert track: %w", err),
			)
		}
	}

	return nil
}

// ReplaceRelease swaps the single saved release for a release group with a
// new one from the same group. Old tracks cascade-delete and linked files
// auto-unlink via ON DELETE SET NULL; the new release and tracks are then
// upserted in the same tx.
func (st *Store) ReplaceRelease(ctx context.Context, groupID string, p FullReleaseParams) error {
	if p.Group.ID != groupID {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("release-group mismatch: release belongs to %q, not %q",
				p.Group.ID,
				groupID,
			),
		)
	}
	tx, err := st.db.BeginTx(ctx, nil)
	if err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("start tx: %w", err),
		)
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx,
		`DELETE FROM releases WHERE release_group_id = ?`,
		groupID,
	)
	if err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("delete release: %w", err),
		)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("delete release: %w", err),
		)
	}
	if n == 0 {
		return apperrors.ErrNotFound
	}

	if err := st.SaveFullRelease(ctx, tx, p); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("commit replace release: %w", err),
		)
	}

	return nil
}
