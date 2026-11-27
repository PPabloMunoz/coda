package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	apperrors "github.com/ppablomunoz/coda/internal/errors"
)

// ScanRun is the durable last completed library scan (done or failed).
// Queued/running states stay memory-only; only terminal runs persist so the
// status survives a restart. FailedFiles keeps the first N failed paths
// (see tasks.MaxScanFailedFiles); FailedCount is the full count.
type ScanRun struct {
	State           string
	QueuedAt        *time.Time
	StartedAt       *time.Time
	FinishedAt      *time.Time
	DurationMs      int64
	Found           int
	Upserted        int
	Pruned          int64
	AutoLinked      int
	Moved           int
	SkippedConflict int
	FailedCount     int
	FailedFiles     []string
	KeptRoots       []string
	IncompleteRoots []string
	Error           string
}

// UpsertScanRun inserts or replaces the singleton last-scan row (id = 1).
func (st *Store) UpsertScanRun(ctx context.Context, run ScanRun) error {
	failedFiles, err := marshalScanPaths(run.FailedFiles)
	if err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("encode scan failed files: %w", err),
		)
	}
	keptRoots, err := marshalScanPaths(run.KeptRoots)
	if err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("encode scan kept roots: %w", err),
		)
	}
	incompleteRoots, err := marshalScanPaths(run.IncompleteRoots)
	if err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("encode scan incomplete roots: %w", err),
		)
	}
	if _, err := st.db.ExecContext(ctx, `
		INSERT INTO scan_runs
			(id, state, queued_at, started_at, finished_at, duration_ms,
			 found, upserted, pruned, autolinked, moved, skipped_conflict,
			 failed_count, failed_files, kept_roots, incomplete_roots, error)
		VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			state=excluded.state,
			queued_at=excluded.queued_at,
			started_at=excluded.started_at,
			finished_at=excluded.finished_at,
			duration_ms=excluded.duration_ms,
			found=excluded.found,
			upserted=excluded.upserted,
			pruned=excluded.pruned,
			autolinked=excluded.autolinked,
			moved=excluded.moved,
			skipped_conflict=excluded.skipped_conflict,
			failed_count=excluded.failed_count,
			failed_files=excluded.failed_files,
			kept_roots=excluded.kept_roots,
			incomplete_roots=excluded.incomplete_roots,
			error=excluded.error`,
		run.State,
		formatImportTime(run.QueuedAt), formatImportTime(run.StartedAt),
		formatImportTime(run.FinishedAt), run.DurationMs,
		run.Found, run.Upserted, run.Pruned,
		run.AutoLinked, run.Moved, run.SkippedConflict,
		run.FailedCount, failedFiles, keptRoots, incompleteRoots, run.Error,
	); err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("upsert scan run: %w", err),
		)
	}
	return nil
}

// GetScanRun returns the last completed scan, or ErrNotFound when no scan
// has completed yet.
func (st *Store) GetScanRun(ctx context.Context) (*ScanRun, error) {
	var run ScanRun
	var queuedAt, startedAt, finishedAt sql.NullString
	var failedFiles, keptRoots, incompleteRoots string
	if err := st.db.QueryRowContext(ctx, `
		SELECT state, queued_at, started_at, finished_at, duration_ms,
			found, upserted, pruned, autolinked, moved, skipped_conflict,
			failed_count, failed_files, kept_roots, incomplete_roots, error
		FROM scan_runs
		WHERE id = 1`,
	).Scan(&run.State, &queuedAt, &startedAt, &finishedAt, &run.DurationMs,
		&run.Found, &run.Upserted, &run.Pruned,
		&run.AutoLinked, &run.Moved, &run.SkippedConflict,
		&run.FailedCount, &failedFiles, &keptRoots, &incompleteRoots, &run.Error,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, apperrors.ErrNotFound
		}
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("query scan run: %w", err),
		)
	}
	queued, err := parseImportTime(queuedAt)
	if err != nil {
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("parse scan run: %w", err),
		)
	}
	started, err := parseImportTime(startedAt)
	if err != nil {
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("parse scan run: %w", err),
		)
	}
	finished, err := parseImportTime(finishedAt)
	if err != nil {
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("parse scan run: %w", err),
		)
	}
	failed, err := unmarshalScanPaths(failedFiles)
	if err != nil {
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("parse scan run: %w", err),
		)
	}
	kept, err := unmarshalScanPaths(keptRoots)
	if err != nil {
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("parse scan run: %w", err),
		)
	}
	incomplete, err := unmarshalScanPaths(incompleteRoots)
	if err != nil {
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("parse scan run: %w", err),
		)
	}
	run.QueuedAt = queued
	run.StartedAt = started
	run.FinishedAt = finished
	run.FailedFiles = failed
	run.KeptRoots = kept
	run.IncompleteRoots = incomplete
	return &run, nil
}

// marshalScanPaths encodes a path list for storage; nil stays '[]'.
func marshalScanPaths(paths []string) (string, error) {
	if len(paths) == 0 {
		return "[]", nil
	}
	raw, err := json.Marshal(paths)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// unmarshalScanPaths decodes a stored path list; ” and '[]' stay nil.
func unmarshalScanPaths(raw string) ([]string, error) {
	if raw == "" || raw == "[]" {
		return nil, nil
	}
	var paths []string
	if err := json.Unmarshal([]byte(raw), &paths); err != nil {
		return nil, err
	}
	return paths, nil
}
