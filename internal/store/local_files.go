package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"

	apperrors "github.com/ppablomunoz/coda/internal/errors"
	"github.com/ppablomunoz/coda/internal/tags"
	"github.com/ppablomunoz/coda/internal/utils"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// ErrConflict is returned when a link/move would violate local_files.path
// uniqueness (destination already tracked).
var ErrConflict = errors.New("[store]: conflict")

// isUniqueViolation reports whether err is SQLite's UNIQUE-constraint
// error, so callers can map it to ErrAlreadyExists without matching
// error strings.
func isUniqueViolation(err error) bool {
	var se *sqlite.Error
	return errors.As(err, &se) && se.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE
}

// activeRootCond hides files whose library root is not active. Removing a
// root from config soft-deactivates it (rows and links are kept for a
// future re-add), so every user-facing file query must filter it out.
const activeRootCond = `root_id IN (SELECT id FROM roots WHERE active = 1)`

// ListFiles returns a filtered page of local files plus the total count.
func (st *Store) ListFiles(
	ctx context.Context,
	opts FileListOptions,
) ([]LocalFile, int, error) {
	conditions := []string{activeRootCond}
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
		return nil, 0, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("count files: %w", err),
		)
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
		return nil, 0, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("query files: %w", err),
		)
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
			return nil, 0, apperrors.Wrap(
				apperrors.ErrInternal,
				fmt.Errorf("scan file: %w", err),
			)
		}

		files = append(files, f)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("reading rows: %w", err),
		)
	}

	if opts.IncludeTrack {
		if err := st.attachFileTracks(ctx, files); err != nil {
			return nil, 0, err
		}
	}

	return files, total, nil
}

// GetFile returns a local file by id, optionally with its linked track context.
func (st *Store) GetFile(ctx context.Context, id int64, includeTrack bool) (*LocalFile, error) {
	f := LocalFile{}

	if err := st.db.QueryRowContext(ctx, `
		SELECT id, root_id, path, size_bytes, mtime_ns, format_hint, scanned_at, track_id
		FROM local_files
		WHERE id = ? AND `+activeRootCond,
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
			return nil, apperrors.ErrNotFound
		}
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("get file: %w", err),
		)
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
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("query file tracks: %w", err),
		)
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
			return apperrors.Wrap(
				apperrors.ErrInternal,
				fmt.Errorf("scan file track: %w", err),
			)
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
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("reading file tracks: %w", err),
		)
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

// ListFilesByRelease returns every tracked file in an active root whose
// linked track belongs to the release. The embed task uses it to re-stamp
// uploaded art; ordering by id keeps retries deterministic.
func (st *Store) ListFilesByRelease(ctx context.Context, releaseID string) ([]LocalFile, error) {
	files := []LocalFile{}
	rows, err := st.db.QueryContext(ctx, `
		SELECT lf.id, lf.root_id, lf.path, lf.size_bytes, lf.mtime_ns,
			lf.format_hint, lf.scanned_at, lf.track_id
		FROM local_files lf
		JOIN tracks t ON t.id = lf.track_id
		WHERE t.release_id = ?
		  AND lf.root_id IN (SELECT id FROM roots WHERE active = 1)
		ORDER BY lf.id`,
		releaseID,
	)
	if err != nil {
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("query files by release: %w", err),
		)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var f LocalFile
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
			return nil, apperrors.Wrap(
				apperrors.ErrInternal,
				fmt.Errorf("scan file by release: %w", err),
			)
		}
		files = append(files, f)
	}
	if err := rows.Err(); err != nil {
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("reading files by release: %w", err),
		)
	}
	return files, nil
}

type LocalFilesStats struct {
	Total    int64            `json:"total"`
	Linked   int64            `json:"linked"`
	Unlinked int64            `json:"unlinked"`
	Formats  map[string]int64 `json:"formats"`
}

// GetLocalFilesStats returns total/linked/unlinked counts plus per-format counts.
func (st *Store) GetLocalFilesStats(ctx context.Context) (*LocalFilesStats, error) {
	res := &LocalFilesStats{}

	if err := st.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM local_files WHERE "+activeRootCond,
	).Scan(&res.Total); err != nil {
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("count total files: %w", err),
		)
	}

	if err := st.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM local_files WHERE track_id IS NOT NULL AND "+activeRootCond,
	).Scan(&res.Linked); err != nil {
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("count linked files: %w", err),
		)
	}

	if err := st.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM local_files WHERE track_id IS NULL AND "+activeRootCond,
	).Scan(&res.Unlinked); err != nil {
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("count unlinked files: %w", err),
		)
	}

	rows, err := st.db.QueryContext(ctx, `
		SELECT format_hint, COUNT(*)
		FROM local_files
		WHERE `+activeRootCond+`
		GROUP BY format_hint`,
	)
	if err != nil {
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("count formats: %w", err),
		)
	}
	defer func() { _ = rows.Close() }()

	res.Formats = map[string]int64{}
	for rows.Next() {
		var format string
		var count int64
		if err := rows.Scan(&format, &count); err != nil {
			return nil, apperrors.Wrap(
				apperrors.ErrInternal,
				fmt.Errorf("scan format count: %w", err),
			)
		}
		res.Formats[format] = count
	}
	if err := rows.Err(); err != nil {
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("reading format counts: %w", err),
		)
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
	// Failed lists paths whose tags could not be read. They are still
	// synced (keeping any existing track link) but never link by MBID,
	// and they don't fail the sync.
	Failed []string
	// KeptRoots lists root paths whose prune was skipped by a fail-safe
	// guard (incomplete walk or unexpected empty result).
	KeptRoots []string
}

// SyncOptions controls prune guards in SyncLocalFiles.
type SyncOptions struct {
	// IncompleteRoots lists root paths whose filesystem walk did not
	// complete (missing/unreadable root or subdirectory). Those roots
	// are never pruned, even when ForcePrune is set.
	IncompleteRoots []string
	// ForcePrune allows pruning a root whose complete walk yielded zero
	// files but still has rows. It never overrides IncompleteRoots.
	ForcePrune bool
}

// SyncLocalFiles upserts scanned files by path (preserving track_id), links
// tracks via on-disk MBIDs, and prunes rows with scanned_at older than scanStart.
//
// Pruning is guarded per root: a root whose walk was incomplete never
// prunes, and a root whose complete walk unexpectedly yields zero files
// keeps its rows (with a warning) unless ForcePrune is set, so an
// unmounted drive, emptied mount point, or permission change cannot wipe
// link data. Normal pruning (individual files genuinely removed) is
// unaffected.
//
// Tag reads happen before the transaction: with the store's single SQLite
// connection, holding a write tx across per-file filesystem I/O would stall
// every other request for the whole scan. Files unchanged since the previous
// scan (same size and mtime) skip the tag parse entirely, so rescanning a
// settled library is DB-cheap.
func (st *Store) SyncLocalFiles(ctx context.Context,
	files []ScannedFile, scanStart string, opts SyncOptions,
) (SyncStats, error) {
	if len(st.rootPaths) == 0 {
		return SyncStats{}, nil
	}
	if strings.TrimSpace(scanStart) == "" {
		return SyncStats{}, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("sync local files: missing scanStart"),
		)
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
		return SyncStats{}, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("get roots: %w", err),
		)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var id int64
		var path string
		if err := rows.Scan(&id, &path); err != nil {
			return SyncStats{}, apperrors.Wrap(
				apperrors.ErrInternal,
				fmt.Errorf("scan roots: %w", err),
			)
		}
		rootIDs[path] = id
	}

	if err := rows.Err(); err != nil {
		return SyncStats{}, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("reading rows: %w", err),
		)
	}

	// Phase 1: on-disk state as of the previous scan, so unchanged files
	// can skip the tag parse.
	known := make(map[string][2]int64, len(files)) // path -> {mtime_ns, size}
	existingCounts := make(map[int64]int64, len(rootIDs))
	ph = placeholders(len(rootIDs))
	ids := make([]any, 0, len(rootIDs))
	for _, id := range rootIDs {
		ids = append(ids, id)
	}
	krows, err := st.db.QueryContext(ctx,
		"SELECT root_id, path, mtime_ns, size_bytes FROM local_files WHERE root_id IN ("+ph+")",
		ids...,
	)
	if err != nil {
		return SyncStats{}, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("query known local files: %w", err),
		)
	}
	for krows.Next() {
		var rootID int64
		var path string
		var mtime, size int64
		if err := krows.Scan(&rootID, &path, &mtime, &size); err != nil {
			_ = krows.Close()
			return SyncStats{}, apperrors.Wrap(
				apperrors.ErrInternal,
				fmt.Errorf("scan known local files: %w", err),
			)
		}
		known[path] = [2]int64{mtime, size}
		existingCounts[rootID]++
	}
	if err := krows.Err(); err != nil {
		_ = krows.Close()
		return SyncStats{}, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("reading known local files: %w", err),
		)
	}
	_ = krows.Close()

	// Phase 2: tag reads outside any tx. A read failure still syncs the
	// file (keeping its existing link) but is reported, never fatal.
	stats := SyncStats{Found: len(files)}

	type row struct {
		rootID  int64
		path    string
		size    int64
		mtime   int64
		hint    string
		trackID string
	}
	batch := make([]row, 0, len(files))
	seen := make(map[string]struct{})
	for _, f := range files {
		id, ok := rootIDs[f.LibraryRoot]
		if !ok {
			return SyncStats{}, apperrors.Wrap(
				apperrors.ErrNotFound,
				fmt.Errorf("unknown root %q", f.LibraryRoot),
			)
		}

		r := row{rootID: id, path: f.Path, size: f.SizeBytes, mtime: f.MtimeNs, hint: f.FormatHint}
		if prev, ok := known[f.Path]; ok && prev[0] == f.MtimeNs && prev[1] == f.SizeBytes {
			batch = append(batch, r) // unchanged since the last scan
			continue
		}

		snap, err := tags.Read(f.Path)
		if err != nil {
			st.log.Warnf("scan sync file=%q read tags: %v", f.Path, err)
			stats.Failed = append(stats.Failed, f.Path)
			batch = append(batch, r)
			continue
		}
		if tid := strings.TrimSpace(snap.MBReleaseTrackID); tid != "" {
			r.trackID = tid
			seen[tid] = struct{}{}
		}
		batch = append(batch, r)
	}

	// Phase 3: one short write tx — all DB work, no filesystem I/O.
	tx, err := st.db.BeginTx(ctx, nil)
	if err != nil {
		return SyncStats{}, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("begin tx local files: %w", err),
		)
	}
	defer func() { _ = tx.Rollback() }()

	exists, err := st.existingTracks(ctx, tx, seen)
	if err != nil {
		return SyncStats{}, err
	}

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
		return SyncStats{}, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("prepare query: %w", err),
		)
	}
	defer func() { _ = stmt.Close() }()

	for _, r := range batch {
		trackID := ""
		if r.trackID != "" {
			if _, ok := exists[r.trackID]; ok {
				trackID = r.trackID
			}
		}

		if _, err := stmt.ExecContext(
			ctx,
			r.rootID,
			r.path,
			r.size,
			r.mtime,
			r.hint,
			scanStart,
			utils.NullIfEmpty(trackID),
		); err != nil {
			return SyncStats{}, apperrors.Wrap(
				apperrors.ErrInternal,
				fmt.Errorf("upsert %q: %w", r.path, err),
			)
		}
		stats.Upserted++
	}

	// Per-root found counts for the empty-root guard.
	foundCounts := make(map[string]int, len(files))
	for _, f := range files {
		foundCounts[f.LibraryRoot]++
	}
	incomplete := make(map[string]struct{}, len(opts.IncompleteRoots))
	for _, r := range opts.IncompleteRoots {
		incomplete[r] = struct{}{}
	}

	for rootPath, id := range rootIDs {
		if _, ok := incomplete[rootPath]; ok {
			st.log.Warnf("scan sync root=%q walk incomplete: keeping %d rows, skipping prune", rootPath, existingCounts[id])
			stats.KeptRoots = append(stats.KeptRoots, rootPath)
			continue
		}
		if foundCounts[rootPath] == 0 && existingCounts[id] > 0 && !opts.ForcePrune {
			st.log.Warnf("scan sync root=%q yielded 0 files but has %d rows: keeping rows, skipping prune (use force prune to clear)", rootPath, existingCounts[id])
			stats.KeptRoots = append(stats.KeptRoots, rootPath)
			continue
		}
		res, err := tx.ExecContext(ctx,
			"DELETE FROM local_files WHERE root_id = ? AND scanned_at < ?",
			id,
			scanStart,
		)
		if err != nil {
			return SyncStats{}, apperrors.Wrap(
				apperrors.ErrInternal,
				fmt.Errorf("[store] prune root %d: %w", id, err),
			)
		}
		if n, err := res.RowsAffected(); err == nil {
			stats.Pruned += n
		}
	}
	sort.Strings(stats.KeptRoots)

	if err := tx.Commit(); err != nil {
		return SyncStats{}, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("commit sync local files: %w", err),
		)
	}

	return stats, nil
}

// existingTracks returns the subset of ids present in tracks, prefetching
// them in chunks inside tx so the sync loop needs no per-file lookup.
func (st *Store) existingTracks(ctx context.Context, tx *sql.Tx, ids map[string]struct{}) (map[string]struct{}, error) {
	out := make(map[string]struct{}, len(ids))
	if len(ids) == 0 {
		return out, nil
	}

	all := make([]string, 0, len(ids))
	for id := range ids {
		all = append(all, id)
	}
	sort.Strings(all)

	const chunk = 500
	for start := 0; start < len(all); start += chunk {
		end := min(start+chunk, len(all))
		ph := placeholders(end - start)
		args := make([]any, 0, end-start)
		for _, id := range all[start:end] {
			args = append(args, id)
		}
		qrows, err := tx.QueryContext(ctx,
			"SELECT id FROM tracks WHERE id IN ("+ph+")", args...)
		if err != nil {
			return nil, apperrors.Wrap(
				apperrors.ErrInternal,
				fmt.Errorf("query existing tracks: %w", err),
			)
		}
		for qrows.Next() {
			var id string
			if err := qrows.Scan(&id); err != nil {
				_ = qrows.Close()
				return nil, apperrors.Wrap(
					apperrors.ErrInternal,
					fmt.Errorf("scan existing tracks: %w", err),
				)
			}
			out[id] = struct{}{}
		}
		if err := qrows.Err(); err != nil {
			_ = qrows.Close()
			return nil, apperrors.Wrap(
				apperrors.ErrInternal,
				fmt.Errorf("reading existing tracks: %w", err),
			)
		}
		_ = qrows.Close()
	}
	return out, nil
}

// GetRoot returns the library root row for id, or ErrNotFound.
func (st *Store) GetRoot(ctx context.Context, id int64) (*Root, error) {
	r := &Root{}
	if err := st.db.QueryRowContext(ctx,
		"SELECT id, path, active, created_at FROM roots WHERE id = ?",
		id).Scan(&r.ID, &r.Path, &r.Active, &r.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, apperrors.ErrNotFound
		}
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("get root: %w", err),
		)
	}
	return r, nil
}

// Link ties a local file to a track and records its current on-disk state.
// newPath/sizeBytes/mtimeNs describe the file AFTER the caller finished tag
// writes and (optionally) the rename, so the DB never points at a path that
// doesn't exist yet. Filesystem work stays in the caller; store is DB-only.
func (st *Store) Link(ctx context.Context,
	fileID int64, trackID, newPath string, sizeBytes, mtimeNs int64,
) error {
	if strings.TrimSpace(newPath) == "" {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("link: empty path"),
		)
	}

	var oldPath string
	if err := st.db.QueryRowContext(ctx,
		"SELECT path FROM local_files WHERE id = ? AND "+activeRootCond,
		fileID).Scan(&oldPath); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return apperrors.ErrNotFound
		}
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("get file: %w", err),
		)
	}

	var tID string
	if err := st.db.QueryRowContext(ctx,
		"SELECT id FROM tracks WHERE id = ?",
		trackID).Scan(&tID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return apperrors.ErrNotFound
		}
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("get track: %w", err),
		)
	}

	if newPath != oldPath {
		var clash int64
		if err := st.db.QueryRowContext(ctx,
			"SELECT id FROM local_files WHERE path = ? AND id != ?",
			newPath, fileID).Scan(&clash); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return apperrors.Wrap(
				apperrors.ErrInternal,
				fmt.Errorf("check path conflict: %w", err),
			)
		} else if err == nil {
			return apperrors.ErrAlreadyExists
		}
	}

	res, err := st.db.ExecContext(ctx, `
		UPDATE local_files
		SET track_id = ?, path = ?, size_bytes = ?, mtime_ns = ?
		WHERE id = ?`,
		trackID, newPath, sizeBytes, mtimeNs, fileID,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return apperrors.ErrAlreadyExists
		}
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("update local_file: %w", err),
		)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return apperrors.ErrNotFound
	}

	return nil
}

// Unlink clears the track link for a local file, keeping the file row.
// A missing or hidden (inactive root) file is ErrNotFound.
func (st *Store) Unlink(ctx context.Context, localFileID int64) error {
	// Update relation
	res, err := st.db.ExecContext(ctx, `
		UPDATE local_files
		SET track_id = NULL
		WHERE id = ? AND `+activeRootCond,
		localFileID,
	)
	if err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("update local_file: %w", err),
		)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return apperrors.ErrNotFound
	}

	return nil
}
