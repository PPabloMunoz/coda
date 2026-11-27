package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

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
	// synced (keeping any existing track link) but are retried on the next
	// scan (tags_read_at stays NULL), and they don't fail the sync.
	Failed []string
	// KeptRoots lists root paths whose prune was skipped by a fail-safe
	// guard (incomplete walk or unexpected empty result).
	KeptRoots []string
	// TagReads counts tag parses attempted (unchanged files skip the read).
	TagReads int
	// Unchanged counts files skipped as settled (same size+mtime with tag
	// evidence already stored).
	Unchanged int
	// TagReadMs is time spent in tag reads, milliseconds.
	TagReadMs int64
}

// syncReadTags is the tag-read seam for SyncFileChunk. Tests stub it to
// count reads without real audio files.
var syncReadTags = tags.Read

// AutoLinkCandidate is a file whose stored tag MBID resolves to a known
// track but whose link differs (unlinked or changed). The auto-link pass
// pages these by id instead of re-reading every file's tags.
type AutoLinkCandidate struct {
	ID        int64
	MbTrackID string
}

// syncUpsertBatch is the chunk size for walk streaming and batched writes.
// One short write transaction covers a chunk, so the single SQLite
// connection is yielded between chunks.
const syncUpsertBatch = 500

// SyncChunkSize is the walk-streaming chunk size (exported for the runner).
const SyncChunkSize = syncUpsertBatch

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

// knownFile is the previous-scan state for one path: on-disk stat plus tag
// evidence. tagsReadAt nil means never read (or a failed read awaiting
// retry), which forces a tag parse even when size+mtime match.
type knownFile struct {
	mtime      int64
	size       int64
	tagsReadAt *string
}

// chunkRow is one file classified for the batched upsert.
type chunkRow struct {
	rootID int64
	path   string
	size   int64
	mtime  int64
	hint   string
	mb     string // validated MBID or "" (NULL)
}

// loadRootIDs resolves configured root paths to their DB ids.
func (st *Store) loadRootIDs(ctx context.Context) (map[string]int64, error) {
	rootIDs := map[string]int64{}
	if len(st.rootPaths) == 0 {
		return rootIDs, nil
	}
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
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("get roots: %w", err),
		)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int64
		var path string
		if err := rows.Scan(&id, &path); err != nil {
			return nil, apperrors.Wrap(
				apperrors.ErrInternal,
				fmt.Errorf("scan roots: %w", err),
			)
		}
		rootIDs[path] = id
	}
	if err := rows.Err(); err != nil {
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("reading rows: %w", err),
		)
	}
	return rootIDs, nil
}

// loadExistingCounts returns per-root row counts before the scan, for the
// empty-root prune guard.
func (st *Store) loadExistingCounts(ctx context.Context, rootIDs map[string]int64) (map[int64]int64, error) {
	out := make(map[int64]int64, len(rootIDs))
	if len(rootIDs) == 0 {
		return out, nil
	}
	ph := placeholders(len(rootIDs))
	ids := make([]any, 0, len(rootIDs))
	for _, id := range rootIDs {
		ids = append(ids, id)
	}
	rows, err := st.db.QueryContext(ctx,
		"SELECT root_id, COUNT(*) FROM local_files WHERE root_id IN ("+ph+") GROUP BY root_id",
		ids...,
	)
	if err != nil {
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("count existing local files: %w", err),
		)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var rootID, n int64
		if err := rows.Scan(&rootID, &n); err != nil {
			return nil, apperrors.Wrap(
				apperrors.ErrInternal,
				fmt.Errorf("scan existing counts: %w", err),
			)
		}
		out[rootID] = n
	}
	if err := rows.Err(); err != nil {
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("reading existing counts: %w", err),
		)
	}
	return out, nil
}

// LoadSyncState resolves root ids and pre-scan row counts for a streaming
// scan. The runner loads this once, then calls SyncFileChunk per walk chunk
// and PruneScanned at the end.
func (st *Store) LoadSyncState(ctx context.Context) (map[string]int64, map[int64]int64, error) {
	rootIDs, err := st.loadRootIDs(ctx)
	if err != nil {
		return nil, nil, err
	}
	counts, err := st.loadExistingCounts(ctx, rootIDs)
	if err != nil {
		return nil, nil, err
	}
	return rootIDs, counts, nil
}

// queryKnownChunk loads previous-scan state for chunk paths only, so
// streaming chunks never load the whole library.
func (st *Store) queryKnownChunk(ctx context.Context, paths []string) (map[string]knownFile, error) {
	out := make(map[string]knownFile, len(paths))
	if len(paths) == 0 {
		return out, nil
	}
	ph := placeholders(len(paths))
	args := make([]any, len(paths))
	for i, p := range paths {
		args[i] = p
	}
	rows, err := st.db.QueryContext(ctx,
		"SELECT path, mtime_ns, size_bytes, tags_read_at FROM local_files WHERE path IN ("+ph+")",
		args...,
	)
	if err != nil {
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("query known local files: %w", err),
		)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var path string
		var mtime, size int64
		var tagsReadAt sql.NullString
		if err := rows.Scan(&path, &mtime, &size, &tagsReadAt); err != nil {
			return nil, apperrors.Wrap(
				apperrors.ErrInternal,
				fmt.Errorf("scan known local files: %w", err),
			)
		}
		k := knownFile{mtime: mtime, size: size}
		if tagsReadAt.Valid {
			s := tagsReadAt.String
			k.tagsReadAt = &s
		}
		out[path] = k
	}
	if err := rows.Err(); err != nil {
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("reading known local files: %w", err),
		)
	}
	return out, nil
}

// SyncFileChunk upserts one bounded walk chunk: tag reads happen outside any
// write tx, then one short tx applies batched multi-row upserts. It never
// touches track_id (linking is the auto-link pass's job); it only stores
// mb_track_id evidence and tags_read_at. Unchanged files (same size+mtime
// with evidence) skip the tag parse. A failed read preserves the old
// evidence but clears tags_read_at so the next scan retries. No pruning.
func (st *Store) SyncFileChunk(ctx context.Context, files []ScannedFile, scanStart string, rootIDs map[string]int64) (SyncStats, error) {
	var stats SyncStats
	if len(files) == 0 {
		return stats, nil
	}
	if strings.TrimSpace(scanStart) == "" {
		return SyncStats{}, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("sync local files: missing scanStart"),
		)
	}
	for _, f := range files {
		if _, ok := rootIDs[f.LibraryRoot]; !ok {
			return SyncStats{}, apperrors.Wrap(
				apperrors.ErrNotFound,
				fmt.Errorf("unknown root %q", f.LibraryRoot),
			)
		}
	}

	paths := make([]string, 0, len(files))
	for _, f := range files {
		paths = append(paths, f.Path)
	}
	known, err := st.queryKnownChunk(ctx, paths)
	if err != nil {
		return SyncStats{}, err
	}

	var unchanged, readOK, readFailed []chunkRow
	for _, f := range files {
		select {
		case <-ctx.Done():
			return SyncStats{}, ctx.Err()
		default:
		}
		id := rootIDs[f.LibraryRoot]
		base := chunkRow{rootID: id, path: f.Path, size: f.SizeBytes, mtime: f.MtimeNs, hint: f.FormatHint}
		if prev, ok := known[f.Path]; ok && prev.mtime == f.MtimeNs && prev.size == f.SizeBytes && prev.tagsReadAt != nil {
			unchanged = append(unchanged, base)
			stats.Unchanged++
			continue
		}
		t0 := time.Now()
		snap, rerr := syncReadTags(f.Path)
		stats.TagReadMs += time.Since(t0).Milliseconds()
		stats.TagReads++
		if rerr != nil {
			st.log.Warnf("scan sync file=%q read tags: %v", f.Path, rerr)
			stats.Failed = append(stats.Failed, f.Path)
			readFailed = append(readFailed, base)
			continue
		}
		if raw := strings.TrimSpace(snap.MBReleaseTrackID); raw != "" {
			if utils.ValidateMBID(raw) == nil {
				base.mb = raw
			}
		}
		readOK = append(readOK, base)
	}

	if err := st.upsertChunkTx(ctx, scanStart, unchanged, readOK, readFailed); err != nil {
		return SyncStats{}, err
	}
	stats.Upserted = len(files)
	return stats, nil
}

// upsertChunkTx applies the three classified groups in one short write tx
// with multi-row statements: unchanged rows only bump scanned_at,
// read-OK rows overwrite mb evidence, failed rows clear tags_read_at to
// force a retry while preserving the old mb and track link.
func (st *Store) upsertChunkTx(ctx context.Context, scanStart string, unchanged, readOK, readFailed []chunkRow) error {
	if len(unchanged) == 0 && len(readOK) == 0 && len(readFailed) == 0 {
		return nil
	}
	tx, err := st.db.BeginTx(ctx, nil)
	if err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("begin tx local files: %w", err),
		)
	}
	defer func() { _ = tx.Rollback() }()

	if len(unchanged) > 0 {
		if err := execUpsert(ctx, tx, scanStart, unchanged, false, false); err != nil {
			return err
		}
	}
	if len(readOK) > 0 {
		if err := execUpsert(ctx, tx, scanStart, readOK, true, false); err != nil {
			return err
		}
	}
	if len(readFailed) > 0 {
		if err := execUpsert(ctx, tx, scanStart, readFailed, false, true); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("commit sync local files: %w", err),
		)
	}
	return nil
}

// execUpsert builds one multi-row INSERT ... ON CONFLICT for rows.
// overwriteMB sets mb_track_id/tags_read_at from the new read; clearTags
// sets tags_read_at=NULL to force a retry (failed reads).
func execUpsert(ctx context.Context, tx *sql.Tx, scanStart string, rows []chunkRow, overwriteMB, clearTags bool) error {
	const cols = "(root_id, path, size_bytes, mtime_ns, format_hint, scanned_at, track_id, mb_track_id, tags_read_at)"
	var sb strings.Builder
	sb.WriteString("INSERT INTO local_files" + cols + " VALUES ")
	args := make([]any, 0, len(rows)*9)
	for i, r := range rows {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString("(?,?,?,?,?,?,?,?,?)")
		args = append(args, r.rootID, r.path, r.size, r.mtime, r.hint, scanStart, nil)
		if overwriteMB {
			args = append(args, utils.NullIfEmpty(r.mb), scanStart)
		} else {
			args = append(args, nil, nil)
		}
	}
	sb.WriteString(` ON CONFLICT(path) DO UPDATE SET
		root_id=excluded.root_id,
		size_bytes=excluded.size_bytes,
		mtime_ns=excluded.mtime_ns,
		format_hint=excluded.format_hint,
		scanned_at=excluded.scanned_at`)
	if overwriteMB {
		sb.WriteString(`, mb_track_id=excluded.mb_track_id,
		tags_read_at=excluded.tags_read_at`)
	} else if clearTags {
		sb.WriteString(`, tags_read_at=NULL`)
	}
	if _, err := tx.ExecContext(ctx, sb.String(), args...); err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("upsert %d files: %w", len(rows), err),
		)
	}
	return nil
}

// PruneScanned deletes rows whose scanned_at predates scanStart, guarded per
// root: incomplete walks never prune, and complete walks yielding zero
// files keep rows unless ForcePrune is set.
func (st *Store) PruneScanned(ctx context.Context, rootIDs map[string]int64, existingCounts map[int64]int64, foundCounts map[string]int, scanStart string, opts SyncOptions) (int64, []string, error) {
	var pruned int64
	var kept []string
	incomplete := make(map[string]struct{}, len(opts.IncompleteRoots))
	for _, r := range opts.IncompleteRoots {
		incomplete[r] = struct{}{}
	}
	for rootPath, id := range rootIDs {
		select {
		case <-ctx.Done():
			return pruned, kept, ctx.Err()
		default:
		}
		if _, ok := incomplete[rootPath]; ok {
			st.log.Warnf("scan sync root=%q walk incomplete: keeping %d rows, skipping prune", rootPath, existingCounts[id])
			kept = append(kept, rootPath)
			continue
		}
		if foundCounts[rootPath] == 0 && existingCounts[id] > 0 && !opts.ForcePrune {
			st.log.Warnf("scan sync root=%q yielded 0 files but has %d rows: keeping rows, skipping prune (use force prune to clear)", rootPath, existingCounts[id])
			kept = append(kept, rootPath)
			continue
		}
		res, err := st.db.ExecContext(ctx,
			"DELETE FROM local_files WHERE root_id = ? AND scanned_at < ?",
			id,
			scanStart,
		)
		if err != nil {
			return pruned, kept, apperrors.Wrap(
				apperrors.ErrInternal,
				fmt.Errorf("[store] prune root %d: %w", id, err),
			)
		}
		if n, err := res.RowsAffected(); err == nil {
			pruned += n
		}
	}
	sort.Strings(kept)
	return pruned, kept, nil
}

// SyncLocalFiles upserts scanned files by path (preserving track_id),
// stores tag evidence for incremental auto-link, and prunes rows with
// scanned_at older than scanStart.
//
// Pruning is guarded per root: a root whose walk was incomplete never
// prunes, and a root whose complete walk unexpectedly yields zero files
// keeps its rows (with a warning) unless ForcePrune is set, so an
// unmounted drive, emptied mount point, or permission change cannot wipe
// link data. Normal pruning (individual files genuinely removed) is
// unaffected.
//
// Tag reads happen outside any write tx: with the store's single SQLite
// connection, holding a write tx across per-file filesystem I/O would stall
// every other request for the whole scan. Files unchanged since the previous
// scan (same size+mtime with tag evidence) skip the tag parse entirely, so
// rescanning a settled library is DB-cheap. Linking is not done here —
// the auto-link pass links via stored mb_track_id — so the write tx is a
// short batched multi-row upsert per chunk.
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
	rootIDs, err := st.loadRootIDs(ctx)
	if err != nil {
		return SyncStats{}, err
	}
	existingCounts, err := st.loadExistingCounts(ctx, rootIDs)
	if err != nil {
		return SyncStats{}, err
	}

	stats := SyncStats{Found: len(files)}
	foundCounts := make(map[string]int, len(rootIDs))
	for _, f := range files {
		foundCounts[f.LibraryRoot]++
	}
	for start := 0; start < len(files); start += syncUpsertBatch {
		select {
		case <-ctx.Done():
			return SyncStats{}, ctx.Err()
		default:
		}
		end := min(start+syncUpsertBatch, len(files))
		chunkStats, err := st.SyncFileChunk(ctx, files[start:end], scanStart, rootIDs)
		if err != nil {
			return SyncStats{}, err
		}
		stats.Upserted += chunkStats.Upserted
		stats.TagReads += chunkStats.TagReads
		stats.Unchanged += chunkStats.Unchanged
		stats.TagReadMs += chunkStats.TagReadMs
		stats.Failed = append(stats.Failed, chunkStats.Failed...)
	}

	pruned, kept, err := st.PruneScanned(ctx, rootIDs, existingCounts, foundCounts, scanStart, opts)
	if err != nil {
		return SyncStats{}, err
	}
	stats.Pruned = pruned
	stats.KeptRoots = kept
	return stats, nil
}

// ListAutoLinkCandidates returns files whose stored tag MBID resolves to a
// known track but whose link differs, paged by id. The scan auto-link pass
// uses this instead of re-reading every file's tags; files whose track is
// not imported yet are excluded by the JOIN until the import lands.
func (st *Store) ListAutoLinkCandidates(ctx context.Context, afterID int64, limit int) ([]AutoLinkCandidate, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := st.db.QueryContext(ctx, `
		SELECT lf.id, lf.mb_track_id
		FROM local_files lf
		JOIN tracks t ON t.id = lf.mb_track_id
		WHERE lf.root_id IN (SELECT id FROM roots WHERE active = 1)
		  AND lf.id > ?
		  AND lf.mb_track_id IS NOT NULL
		  AND (lf.track_id IS NULL OR lf.track_id <> lf.mb_track_id)
		ORDER BY lf.id LIMIT ?`,
		afterID, limit,
	)
	if err != nil {
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("query auto-link candidates: %w", err),
		)
	}
	defer func() { _ = rows.Close() }()
	out := []AutoLinkCandidate{}
	for rows.Next() {
		var c AutoLinkCandidate
		if err := rows.Scan(&c.ID, &c.MbTrackID); err != nil {
			return nil, apperrors.Wrap(
				apperrors.ErrInternal,
				fmt.Errorf("scan auto-link candidate: %w", err),
			)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("reading auto-link candidates: %w", err),
		)
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
// doesn't exist yet. It also stores the tag evidence (mb_track_id and
// tags_read_at) so settled rescans skip the tag parse. Filesystem work
// stays in the caller; store is DB-only.
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
		SET track_id = ?, path = ?, size_bytes = ?, mtime_ns = ?,
			mb_track_id = ?, tags_read_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
		WHERE id = ?`,
		trackID, newPath, sizeBytes, mtimeNs, trackID, fileID,
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
// It also clears the stored tag evidence (the caller just cleared the
// tags), so the next scan re-reads rather than trusting stale MBIDs.
// A missing or hidden (inactive root) file is ErrNotFound.
func (st *Store) Unlink(ctx context.Context, localFileID int64) error {
	// Update relation
	res, err := st.db.ExecContext(ctx, `
		UPDATE local_files
		SET track_id = NULL, mb_track_id = NULL, tags_read_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
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
