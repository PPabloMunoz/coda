package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/ppablomunoz/coda/internal/tags"
	"github.com/ppablomunoz/coda/internal/utils"
)

func (st *Store) ListFiles(
	ctx context.Context,
	opts FileListOptions,
) ([]LocalFile, int, error) {
	conditions := []string{}
	args := []any{}

	if opts.Linked != nil {
		if *opts.Linked {
			conditions = append(conditions, "track_id IS NOT NULL")
		} else {
			conditions = append(conditions, "track_id IS NULL")
		}
	}
	if query := strings.TrimSpace(opts.Query); query != "" {
		conditions = append(conditions, `unaccent(path) LIKE unaccent(?) ESCAPE '\'`)
		args = append(args, likePattern(query))
	}
	if format := strings.TrimSpace(opts.Format); format != "" {
		conditions = append(conditions, "format_hint = ? COLLATE NOCASE")
		args = append(args, format)
	}

	where := ""
	if len(conditions) > 0 {
		where = " WHERE " + strings.Join(conditions, " AND ")
	}

	var total int
	if err := st.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM local_files"+where,
		args...,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("[store] count files: %w", err)
	}

	order := sortExpression(opts.Sort, map[string]string{
		"path":       "unaccent(path)",
		"size":       "size_bytes",
		"scanned_at": "scanned_at",
		"format":     "unaccent(format_hint)",
	}, "path") + " " + orderDirection(opts.Order)

	queryArgs := append(append([]any{}, args...), opts.Limit, opts.Offset)
	rows, err := st.db.QueryContext(ctx, `
		SELECT id, root_id, path, size_bytes, mtime_ns, format_hint, scanned_at, track_id
		FROM local_files`+where+`
		ORDER BY `+order+`
		LIMIT ? OFFSET ?`,
		queryArgs...,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("[store] query files: %w", err)
	}
	defer func() { _ = rows.Close() }()

	files := []LocalFile{}

	for rows.Next() {
		f := LocalFile{}

		if err := rows.Scan(
			&f.ID,
			&f.RootID,
			&f.Path,
			&f.SizeBytes,
			&f.MtimeNs,
			&f.FormatHint,
			&f.ScannedAt,
			&f.TrackID,
		); err != nil {
			return nil, 0, fmt.Errorf("[store] scan file: %w", err)
		}

		files = append(files, f)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("[store] reading rows: %w", err)
	}

	if opts.IncludeTrack {
		if err := st.attachFileTracks(ctx, files); err != nil {
			return nil, 0, err
		}
	}

	return files, total, nil
}

func (st *Store) GetFile(ctx context.Context, id int64, includeTrack bool) (*LocalFile, error) {
	f := LocalFile{}

	if err := st.db.QueryRowContext(ctx, `
		SELECT id, root_id, path, size_bytes, mtime_ns, format_hint, scanned_at, track_id
		FROM local_files
		WHERE id = ?`,
		id,
	).Scan(
		&f.ID,
		&f.RootID,
		&f.Path,
		&f.SizeBytes,
		&f.MtimeNs,
		&f.FormatHint,
		&f.ScannedAt,
		&f.TrackID,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("[store] get file: %w", err)
	}

	if includeTrack {
		files := []LocalFile{f}
		if err := st.attachFileTracks(ctx, files); err != nil {
			return nil, err
		}
		f = files[0]
	}

	return &f, nil
}

// attachFileTracks adds track, release, and release-group context to files in
// one query. Files without a track are left untouched.
func (st *Store) attachFileTracks(ctx context.Context, files []LocalFile) error {
	trackIDs := make(map[string]struct{})
	for i := range files {
		if files[i].TrackID != nil {
			trackIDs[*files[i].TrackID] = struct{}{}
		}
	}
	if len(trackIDs) == 0 {
		return nil
	}

	ph := placeholders(len(trackIDs))
	args := make([]any, 0, len(trackIDs))
	for id := range trackIDs {
		args = append(args, id)
	}

	rows, err := st.db.QueryContext(ctx, `
		SELECT t.id, t.title, t.disc_number, t.position, t.number,
			t.recording_id, t.artist_credit_id,
			r.id, r.title, r.date, r.status,
			rg.id, rg.title, rg.type
		FROM tracks t
		JOIN releases r ON r.id = t.release_id
		JOIN release_groups rg ON rg.id = r.release_group_id
		WHERE t.id IN (`+ph+`)`,
		args...,
	)
	if err != nil {
		return fmt.Errorf("[store] query file tracks: %w", err)
	}
	defer func() { _ = rows.Close() }()

	byTrack := make(map[string]*LinkedTrack, len(trackIDs))
	creditIDs := make(map[int64]struct{})
	creditByTrack := make(map[string]int64)

	for rows.Next() {
		var track LinkedTrack
		var creditID sql.NullInt64
		var date, status sql.NullString

		if err := rows.Scan(
			&track.ID,
			&track.Title,
			&track.Disc,
			&track.Position,
			&track.Number,
			&track.RecordingID,
			&creditID,
			&track.Release.ID,
			&track.Release.Title,
			&date,
			&status,
			&track.Group.ID,
			&track.Group.Title,
			&track.Group.Type,
		); err != nil {
			return fmt.Errorf("[store] scan file track: %w", err)
		}

		track.Release.Date = nullString(date)
		track.Release.Status = nullString(status)

		if creditID.Valid {
			creditIDs[creditID.Int64] = struct{}{}
			creditByTrack[track.ID] = creditID.Int64
		}

		byTrack[track.ID] = &track
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("[store] reading file tracks: %w", err)
	}

	if len(creditIDs) > 0 {
		credits, err := st.getCreditEntries(ctx, creditIDs)
		if err != nil {
			return err
		}
		for trackID, creditID := range creditByTrack {
			if track, ok := byTrack[trackID]; ok {
				track.Credit = credits[creditID]
			}
		}
	}

	for i := range files {
		if files[i].TrackID == nil {
			continue
		}
		if track, ok := byTrack[*files[i].TrackID]; ok {
			files[i].Track = track
		}
	}

	return nil
}

type LocalFilesStats struct {
	Total    int64            `json:"total"`
	Linked   int64            `json:"linked"`
	Unlinked int64            `json:"unlinked"`
	Formats  map[string]int64 `json:"formats"`
}

func (st *Store) GetLocalFilesStats(ctx context.Context) (*LocalFilesStats, error) {
	res := &LocalFilesStats{}

	if err := st.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM local_files",
	).Scan(&res.Total); err != nil {
		return nil, fmt.Errorf("[store] count files: %w", err)
	}

	if err := st.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM local_files WHERE track_id IS NOT NULL",
	).Scan(&res.Linked); err != nil {
		return nil, fmt.Errorf("[store] count files: %w", err)
	}

	if err := st.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM local_files WHERE track_id IS NULL",
	).Scan(&res.Unlinked); err != nil {
		return nil, fmt.Errorf("[store] count files: %w", err)
	}

	rows, err := st.db.QueryContext(ctx, `
		SELECT format_hint, COUNT(*)
		FROM local_files
		GROUP BY format_hint`,
	)
	if err != nil {
		return nil, fmt.Errorf("[store] count formats: %w", err)
	}
	defer func() { _ = rows.Close() }()

	res.Formats = map[string]int64{}
	for rows.Next() {
		var format string
		var count int64
		if err := rows.Scan(&format, &count); err != nil {
			return nil, fmt.Errorf("[store] scan format count: %w", err)
		}
		res.Formats[format] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("[store] reading format counts: %w", err)
	}

	return res, nil
}

type ScannedFile struct {
	LibraryRoot string
	Path        string
	SizeBytes   int64
	MtimeNs     int64
	FormatHint  string
}

type SyncStats struct {
	Found    int
	Upserted int
	Pruned   int64
}

func (st *Store) SyncLocalFiles(ctx context.Context, files []ScannedFile, scanStart string) (SyncStats, error) {
	if len(st.rootPaths) == 0 {
		return SyncStats{}, nil
	}
	if strings.TrimSpace(scanStart) == "" {
		return SyncStats{}, fmt.Errorf("[store] sync local files: missing scanStart")
	}

	rootIDs := map[string]int64{}
	ph := placeholders(len(st.rootPaths))
	args := make([]any, len(st.rootPaths))

	for i, r := range st.rootPaths {
		args[i] = r
	}

	rows, err := st.db.QueryContext(ctx,
		"SELECT id, path FROM roots WHERE path in ("+ph+")",
		args...,
	)
	if err != nil {
		return SyncStats{}, fmt.Errorf("[store] get roots: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var id int64
		var path string
		if err := rows.Scan(&id, &path); err != nil {
			return SyncStats{}, fmt.Errorf("[store] scan roots: %w", err)
		}
		rootIDs[path] = id
	}

	if err := rows.Err(); err != nil {
		return SyncStats{}, fmt.Errorf("[store] reading rows: %w", err)
	}

	tx, err := st.db.BeginTx(ctx, nil)
	if err != nil {
		return SyncStats{}, fmt.Errorf("[store] begin tx local files: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO local_files(root_id, path, size_bytes,
			mtime_ns, format_hint, scanned_at, track_id)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET
			root_id=excluded.root_id,
			size_bytes=excluded.size_bytes,
			mtime_ns=excluded.mtime_ns,
			format_hint=excluded.format_hint,
			scanned_at=excluded.scanned_at,
			track_id=COALESCE(excluded.track_id, local_files.track_id)`,
	)
	if err != nil {
		return SyncStats{}, st.prepareQueryError(err)
	}
	defer func() { _ = stmt.Close() }()

	stats := SyncStats{Found: len(files)}
	for _, f := range files {
		id, ok := rootIDs[f.LibraryRoot]
		if !ok {
			return SyncStats{}, fmt.Errorf("[store] unknown root %q", f.LibraryRoot)
		}

		snap, err := tags.Read(f.Path)
		if err != nil {
			return SyncStats{}, fmt.Errorf("[store] read file tags => %q: %w", f.Path, err)
		}

		exists, err := st.trackExists(ctx, tx, snap.MBReleaseTrackID)
		if err != nil {
			return SyncStats{}, fmt.Errorf("[store] check track exists: %w", err)
		}

		trackID := ""
		if exists {
			trackID = snap.MBReleaseTrackID
		}

		if _, err := stmt.ExecContext(
			ctx,
			id,
			f.Path,
			f.SizeBytes,
			f.MtimeNs,
			f.FormatHint,
			scanStart,
			utils.NullIfEmpty(trackID),
		); err != nil {
			return SyncStats{}, fmt.Errorf("[store] upsert %q: %w", f.Path, err)
		}
		stats.Upserted++
	}

	for _, id := range rootIDs {
		res, err := tx.ExecContext(ctx,
			"DELETE FROM local_files WHERE root_id = ? AND scanned_at < ?",
			id,
			scanStart,
		)
		if err != nil {
			return SyncStats{}, fmt.Errorf("[store] prune root %d: %w", id, err)
		}
		if n, err := res.RowsAffected(); err == nil {
			stats.Pruned += n
		}
	}

	if err := tx.Commit(); err != nil {
		return SyncStats{}, fmt.Errorf("[store] commit sync local files: %w", err)
	}

	return stats, nil
}

func (st *Store) Link(ctx context.Context, localFileID int64, trackID string) error {
	// Local File exists?
	var lID int64
	if err := st.db.QueryRowContext(ctx,
		"SELECT id FROM local_files WHERE id = ?",
		localFileID).Scan(&lID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}

	// Track exists?
	var tID string
	if err := st.db.QueryRowContext(ctx,
		"SELECT id FROM tracks WHERE id = ?",
		trackID).Scan(&tID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}

	// Update relation
	_, err := st.db.ExecContext(ctx, `
		UPDATE local_files
		SET track_id = ?
		WHERE id = ?`,
		trackID, localFileID,
	)
	if err != nil {
		return fmt.Errorf("[store] update local_file: %w", err)
	}

	return nil
}

func (st *Store) Unlink(ctx context.Context, localFileID int64) error {
	// Update relation
	_, err := st.db.ExecContext(ctx, `
		UPDATE local_files
		SET track_id = NULL
		WHERE id = ?`,
		localFileID,
	)
	if err != nil {
		return fmt.Errorf("[store] update local_file: %w", err)
	}

	return nil
}
