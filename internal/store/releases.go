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
// If the group already stores a different release (a user-selected switch),
// the stored release is preserved and the incoming one is skipped so a
// later discography refresh never overwrites the selection or aborts on the
// UNIQUE(release_group_id) constraint. Tracks are reconciled: vanished
// tracks are deleted (unlinking their files), duplicate positions in one
// batch are skipped, and surviving tracks are parked out of the way before
// upsert so a changed tracklist never hits UNIQUE(release_id, disc, position).
// An empty (or fully invalid) incoming tracklist leaves existing tracks alone.
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

	var storedID string
	if err := tx.QueryRowContext(ctx,
		`SELECT id FROM releases WHERE release_group_id = ?`,
		p.Group.ID,
	).Scan(&storedID); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("query stored release: %w", err),
		)
	} else if err == nil && storedID != p.ID {
		return nil
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

	type validTrack struct {
		TrackParams
		disc int
	}
	valid := make([]validTrack, 0, len(p.Tracks))
	seenPos := make(map[[2]int]struct{}, len(p.Tracks))
	seenID := make(map[string]struct{}, len(p.Tracks))
	for _, t := range p.Tracks {
		if t.Position < 1 || strings.TrimSpace(t.Title) == "" {
			continue
		}
		if _, ok := seenID[t.ID]; ok {
			continue
		}
		disc := max(t.Disc, 1)
		key := [2]int{disc, t.Position}
		if _, ok := seenPos[key]; ok {
			continue
		}
		seenID[t.ID] = struct{}{}
		seenPos[key] = struct{}{}
		valid = append(valid, validTrack{TrackParams: t, disc: disc})
	}

	if len(valid) == 0 {
		return nil
	}

	rows, err := tx.QueryContext(ctx,
		`SELECT id, disc_number, position FROM tracks WHERE release_id = ?`,
		p.ID,
	)
	if err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("query existing tracks: %w", err),
		)
	}
	defer func() { _ = rows.Close() }()
	existing := make(map[string]struct{}, len(valid))
	maxPos := 0
	for rows.Next() {
		var id string
		var disc, pos int
		if err := rows.Scan(&id, &disc, &pos); err != nil {
			_ = rows.Close()
			return apperrors.Wrap(
				apperrors.ErrInternal,
				fmt.Errorf("scan existing track: %w", err),
			)
		}
		existing[id] = struct{}{}
		if pos > maxPos {
			maxPos = pos
		}
	}
	if err := rows.Err(); err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("reading existing tracks: %w", err),
		)
	}
	for _, t := range valid {
		if t.Position > maxPos {
			maxPos = t.Position
		}
	}

	incoming := make(map[string]struct{}, len(valid))
	for _, t := range valid {
		incoming[t.ID] = struct{}{}
	}

	if len(incoming) > 0 {
		args := make([]any, 0, len(incoming)+1)
		args = append(args, p.ID)
		for id := range incoming {
			args = append(args, id)
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM tracks WHERE release_id = ? AND id NOT IN (`+placeholders(len(incoming))+`)`,
			args...,
		); err != nil {
			return apperrors.Wrap(
				apperrors.ErrInternal,
				fmt.Errorf("reconcile tracks delete: %w", err),
			)
		}
	}

	// Explicitly unlink files of removed tracks. Production runs with
	// foreign_keys=ON so the cascade already nulled them; the explicit
	// update keeps behavior identical when FK enforcement is off (tests)
	// and never touches surviving links.
	deleted := make([]any, 0)
	for id := range existing {
		if _, ok := incoming[id]; !ok {
			deleted = append(deleted, id)
		}
	}
	if len(deleted) > 0 {
		if _, err := tx.ExecContext(ctx,
			`UPDATE local_files SET track_id = NULL WHERE track_id IN (`+placeholders(len(deleted))+`)`,
			deleted...,
		); err != nil {
			return apperrors.Wrap(
				apperrors.ErrInternal,
				fmt.Errorf("unlink removed tracks: %w", err),
			)
		}
	}

	survivors := 0
	for id := range existing {
		if _, ok := incoming[id]; ok {
			survivors++
		}
	}
	if survivors > 0 {
		offset := maxPos + 100000
		if _, err := tx.ExecContext(ctx,
			`UPDATE tracks SET position = position + ? WHERE release_id = ?`,
			offset, p.ID,
		); err != nil {
			return apperrors.Wrap(
				apperrors.ErrInternal,
				fmt.Errorf("park surviving tracks: %w", err),
			)
		}
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

	for _, t := range valid {
		trackCreditID, err := createCreditTx(ctx, tx, t.Credit)
		if err != nil {
			return err
		}

		if _, err := stmt.ExecContext(ctx,
			t.ID, p.ID, t.disc, t.Position, t.Number,
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
// new one from the same group. File links whose recording + disc/position
// match a track on the new release are remapped in the same tx so they
// survive the switch; the rest unlink via ON DELETE SET NULL. The new
// release and tracks are then upserted, and a background task rewrites
// tags, covers, and paths for the remapped files.
func (st *Store) ReplaceRelease(ctx context.Context, groupID string, p FullReleaseParams) error {
	if p.Group.ID != groupID {
		return apperrors.Wrap(
			apperrors.ErrInvalidInput,
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

	var groupIgnored bool
	if err := tx.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM ignored WHERE id = ? AND type = 'release_group')`, groupID,
	).Scan(&groupIgnored); err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("check ignored group: %w", err),
		)
	}
	if groupIgnored {
		return apperrors.ErrNotFound
	}

	type oldLink struct {
		fileID int64
		rec    string
		disc   int
		pos    int
	}
	// Scanned in a closure so rows close via defer before the DELETE
	// below runs: the store holds a single connection, so the rows must
	// be released before the next statement on the tx.
	oldLinks, err := func() ([]oldLink, error) {
		linkRows, err := tx.QueryContext(ctx, `
		SELECT lf.id, t.recording_id, t.disc_number, t.position
		FROM local_files lf
		JOIN tracks t ON t.id = lf.track_id
		JOIN releases r ON r.id = t.release_id
		WHERE r.release_group_id = ? AND lf.track_id IS NOT NULL`,
			groupID,
		)
		if err != nil {
			return nil, apperrors.Wrap(
				apperrors.ErrInternal,
				fmt.Errorf("query outgoing links: %w", err),
			)
		}
		defer func() { _ = linkRows.Close() }()
		var out []oldLink
		for linkRows.Next() {
			var l oldLink
			var rec sql.NullString
			if err := linkRows.Scan(&l.fileID, &rec, &l.disc, &l.pos); err != nil {
				return nil, apperrors.Wrap(
					apperrors.ErrInternal,
					fmt.Errorf("scan outgoing link: %w", err),
				)
			}
			if rec.Valid {
				l.rec = strings.TrimSpace(rec.String)
			}
			out = append(out, l)
		}
		if err := linkRows.Err(); err != nil {
			return nil, apperrors.Wrap(
				apperrors.ErrInternal,
				fmt.Errorf("reading outgoing links: %w", err),
			)
		}
		return out, nil
	}()
	if err != nil {
		return err
	}

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

	// Explicitly unlink outgoing files. Production runs with
	// foreign_keys=ON so the cascade already nulled them; the explicit
	// update keeps behavior identical when FK enforcement is off (tests).
	if len(oldLinks) > 0 {
		ids := make([]any, 0, len(oldLinks))
		for _, l := range oldLinks {
			ids = append(ids, l.fileID)
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE local_files SET track_id = NULL WHERE id IN (`+placeholders(len(ids))+`)`,
			ids...,
		); err != nil {
			return apperrors.Wrap(
				apperrors.ErrInternal,
				fmt.Errorf("unlink outgoing files: %w", err),
			)
		}
	}

	if err := st.SaveFullRelease(ctx, tx, p); err != nil {
		return err
	}

	if err := suppressIgnoredCollaboratorsTx(ctx, tx); err != nil {
		return err
	}

	if len(oldLinks) > 0 {
		type trackKey struct {
			rec  string
			disc int
			pos  int
		}
		// Scanned in a closure so rows close via defer before the
		// per-file UPDATEs below run on the single-connection tx.
		byKey, err := func() (map[trackKey]string, error) {
			newRows, err := tx.QueryContext(ctx,
				`SELECT id, recording_id, disc_number, position FROM tracks WHERE release_id = ?`,
				p.ID,
			)
			if err != nil {
				return nil, apperrors.Wrap(
					apperrors.ErrInternal,
					fmt.Errorf("query new tracks: %w", err),
				)
			}
			defer func() { _ = newRows.Close() }()
			byKey := make(map[trackKey]string)
			for newRows.Next() {
				var id string
				var rec sql.NullString
				var disc, pos int
				if err := newRows.Scan(&id, &rec, &disc, &pos); err != nil {
					return nil, apperrors.Wrap(
						apperrors.ErrInternal,
						fmt.Errorf("scan new track: %w", err),
					)
				}
				recStr := ""
				if rec.Valid {
					recStr = strings.TrimSpace(rec.String)
				}
				if recStr == "" {
					continue
				}
				byKey[trackKey{rec: recStr, disc: disc, pos: pos}] = id
			}
			if err := newRows.Err(); err != nil {
				return nil, apperrors.Wrap(
					apperrors.ErrInternal,
					fmt.Errorf("reading new tracks: %w", err),
				)
			}
			return byKey, nil
		}()
		if err != nil {
			return err
		}

		for _, l := range oldLinks {
			if strings.TrimSpace(l.rec) == "" {
				continue
			}
			newID, ok := byKey[trackKey{rec: l.rec, disc: l.disc, pos: l.pos}]
			if !ok {
				continue
			}
			if _, err := tx.ExecContext(ctx,
				`UPDATE local_files SET track_id = ? WHERE id = ?`,
				newID, l.fileID,
			); err != nil {
				return apperrors.Wrap(
					apperrors.ErrInternal,
					fmt.Errorf("remap file link: %w", err),
				)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("commit replace release: %w", err),
		)
	}

	return nil
}
