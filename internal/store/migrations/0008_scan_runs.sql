-- 0008_scan_runs.sql — Durable last-scan diagnostics.
-- The scan queue is in-memory, so a restart lost the last run's outcome.
-- `scan_runs` keeps a single row (id = 1) with the last completed scan
-- (done or failed): state, timestamps, duration, SyncStats/AutoLinkStats
-- counters, and JSON-encoded failed/kept/incomplete path lists. Queued and
-- running states stay memory-only; a restart rehydrates the last terminal
-- state and the startup scan re-queues fresh work.

-- +goose Up
CREATE TABLE scan_runs (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  state TEXT NOT NULL CHECK (state IN ('done', 'failed')),
  queued_at TEXT,
  started_at TEXT,
  finished_at TEXT,
  duration_ms INTEGER NOT NULL DEFAULT 0,
  found INTEGER NOT NULL DEFAULT 0,
  upserted INTEGER NOT NULL DEFAULT 0,
  pruned INTEGER NOT NULL DEFAULT 0,
  autolinked INTEGER NOT NULL DEFAULT 0,
  moved INTEGER NOT NULL DEFAULT 0,
  skipped_conflict INTEGER NOT NULL DEFAULT 0,
  failed_count INTEGER NOT NULL DEFAULT 0,
  failed_files TEXT NOT NULL DEFAULT '[]',
  kept_roots TEXT NOT NULL DEFAULT '[]',
  incomplete_roots TEXT NOT NULL DEFAULT '[]',
  error TEXT NOT NULL DEFAULT ''
);

-- +goose Down
DROP TABLE IF EXISTS scan_runs;
