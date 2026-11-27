package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	apperrors "github.com/ppablomunoz/coda/internal/errors"
)

// Pending release-task kinds. They mirror the queue's embed/clear/relink
// tasks: 'embed' re-stamps the cached cover into linked files, 'clear'
// wipes it after a removal, 'relink' rewrites tags/cover/paths after a
// release switch.
const (
	PendingTaskEmbed  = "embed"
	PendingTaskClear  = "clear"
	PendingTaskRelink = "relink"
)

// ArtistImport is the durable import lifecycle for one artist. Queued and
// running rows are resumable across restarts; done and failed rows stay
// visible instead of resetting to idle.
type ArtistImport struct {
	ArtistID   string
	State      string
	QueuedAt   *time.Time
	StartedAt  *time.Time
	FinishedAt *time.Time
	Error      string
}

// PendingReleaseTask is one durable file job for a release. One row covers
// repeated enqueues: the task reloads bytes/links at run time.
type PendingReleaseTask struct {
	ID        int64
	ReleaseID string
	Kind      string
	QueuedAt  time.Time
}

// UpsertArtistImport inserts or replaces the import row for an artist.
func (st *Store) UpsertArtistImport(ctx context.Context, imp ArtistImport) error {
	if _, err := st.db.ExecContext(ctx, `
		INSERT INTO artist_imports
			(artist_id, state, queued_at, started_at, finished_at, error)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(artist_id) DO UPDATE SET
			state=excluded.state,
			queued_at=excluded.queued_at,
			started_at=excluded.started_at,
			finished_at=excluded.finished_at,
			error=excluded.error`,
		imp.ArtistID, imp.State,
		formatImportTime(imp.QueuedAt), formatImportTime(imp.StartedAt),
		formatImportTime(imp.FinishedAt), imp.Error,
	); err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("upsert artist import: %w", err),
		)
	}
	return nil
}

// GetArtistImport returns the import row for an artist, or ErrNotFound.
func (st *Store) GetArtistImport(ctx context.Context, artistID string) (*ArtistImport, error) {
	var imp ArtistImport
	var queuedAt, startedAt, finishedAt sql.NullString
	if err := st.db.QueryRowContext(ctx, `
		SELECT artist_id, state, queued_at, started_at, finished_at, error
		FROM artist_imports
		WHERE artist_id = ?`, artistID,
	).Scan(&imp.ArtistID, &imp.State, &queuedAt, &startedAt, &finishedAt, &imp.Error); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, apperrors.ErrNotFound
		}
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("query artist import: %w", err),
		)
	}
	queued, err := parseImportTime(queuedAt)
	if err != nil {
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("parse artist import: %w", err),
		)
	}
	started, err := parseImportTime(startedAt)
	if err != nil {
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("parse artist import: %w", err),
		)
	}
	finished, err := parseImportTime(finishedAt)
	if err != nil {
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("parse artist import: %w", err),
		)
	}
	imp.QueuedAt = queued
	imp.StartedAt = started
	imp.FinishedAt = finished
	return &imp, nil
}

// ListArtistImports returns all import rows for startup recovery.
func (st *Store) ListArtistImports(ctx context.Context) ([]ArtistImport, error) {
	rows, err := st.db.QueryContext(ctx, `
		SELECT artist_id, state, queued_at, started_at, finished_at, error
		FROM artist_imports`)
	if err != nil {
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("list artist imports: %w", err),
		)
	}
	defer func() { _ = rows.Close() }()

	imports := []ArtistImport{}
	for rows.Next() {
		var imp ArtistImport
		var queuedAt, startedAt, finishedAt sql.NullString
		if err := rows.Scan(&imp.ArtistID, &imp.State,
			&queuedAt, &startedAt, &finishedAt, &imp.Error,
		); err != nil {
			return nil, apperrors.Wrap(
				apperrors.ErrInternal,
				fmt.Errorf("scan artist import: %w", err),
			)
		}
		queued, err := parseImportTime(queuedAt)
		if err != nil {
			return nil, apperrors.Wrap(
				apperrors.ErrInternal,
				fmt.Errorf("parse artist import: %w", err),
			)
		}
		started, err := parseImportTime(startedAt)
		if err != nil {
			return nil, apperrors.Wrap(
				apperrors.ErrInternal,
				fmt.Errorf("parse artist import: %w", err),
			)
		}
		finished, err := parseImportTime(finishedAt)
		if err != nil {
			return nil, apperrors.Wrap(
				apperrors.ErrInternal,
				fmt.Errorf("parse artist import: %w", err),
			)
		}
		imp.QueuedAt = queued
		imp.StartedAt = started
		imp.FinishedAt = finished
		imports = append(imports, imp)
	}
	if err := rows.Err(); err != nil {
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("reading artist imports: %w", err),
		)
	}
	return imports, nil
}

// DeleteArtistImport removes the import row for an artist. It is idempotent.
func (st *Store) DeleteArtistImport(ctx context.Context, artistID string) error {
	if _, err := st.db.ExecContext(ctx,
		`DELETE FROM artist_imports WHERE artist_id = ?`, artistID,
	); err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("delete artist import: %w", err),
		)
	}
	return nil
}

// InsertPendingReleaseTask records a pending file job, coalescing repeats:
// one row covers repeated enqueues because the task reloads bytes/links at
// run time. An existing row is refreshed, never duplicated.
func (st *Store) InsertPendingReleaseTask(ctx context.Context, releaseID, kind string) error {
	if _, err := st.db.ExecContext(ctx, `
		INSERT INTO pending_release_tasks (release_id, kind, queued_at)
		VALUES (?, ?, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
		ON CONFLICT(release_id, kind) DO UPDATE SET
			queued_at=strftime('%Y-%m-%dT%H:%M:%fZ', 'now')`,
		releaseID, kind,
	); err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("insert pending release task: %w", err),
		)
	}
	return nil
}

// ListPendingReleaseTasks returns pending file jobs in FIFO (insertion) order.
func (st *Store) ListPendingReleaseTasks(ctx context.Context) ([]PendingReleaseTask, error) {
	rows, err := st.db.QueryContext(ctx, `
		SELECT id, release_id, kind, queued_at
		FROM pending_release_tasks
		ORDER BY id`)
	if err != nil {
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("list pending release tasks: %w", err),
		)
	}
	defer func() { _ = rows.Close() }()

	tasks := []PendingReleaseTask{}
	for rows.Next() {
		var t PendingReleaseTask
		var queuedAt string
		if err := rows.Scan(&t.ID, &t.ReleaseID, &t.Kind, &queuedAt); err != nil {
			return nil, apperrors.Wrap(
				apperrors.ErrInternal,
				fmt.Errorf("scan pending release task: %w", err),
			)
		}
		parsed, err := time.Parse(time.RFC3339Nano, queuedAt)
		if err != nil {
			return nil, apperrors.Wrap(
				apperrors.ErrInternal,
				fmt.Errorf("parse pending release task: %w", err),
			)
		}
		t.QueuedAt = parsed.UTC()
		tasks = append(tasks, t)
	}
	if err := rows.Err(); err != nil {
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("reading pending release tasks: %w", err),
		)
	}
	return tasks, nil
}

// DeletePendingReleaseTask removes the pending file job for a release and
// kind. It is idempotent.
func (st *Store) DeletePendingReleaseTask(ctx context.Context, releaseID, kind string) error {
	if _, err := st.db.ExecContext(ctx,
		`DELETE FROM pending_release_tasks WHERE release_id = ? AND kind = ?`,
		releaseID, kind,
	); err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("delete pending release task: %w", err),
		)
	}
	return nil
}

// formatImportTime renders an optional timestamp for storage (RFC3339Nano,
// UTC); nil stays NULL.
func formatImportTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(time.RFC3339Nano)
}

// parseImportTime parses a nullable stored timestamp; NULL stays nil.
func parseImportTime(s sql.NullString) (*time.Time, error) {
	if !s.Valid || s.String == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, s.String)
	if err != nil {
		return nil, err
	}
	utc := parsed.UTC()
	return &utc, nil
}
