-- 0007_background_tasks.sql — Durable background-task intent.
-- The task queue is in-memory, so a restart lost queued work and reset
-- import statuses to idle. These tables persist the intent instead:
-- `artist_imports` records every import lifecycle (queued/running stay
-- resumable, done/failed stay visible), and `pending_release_tasks`
-- records one pending file job per (release, kind) — embeds, clears, and
-- relinks all reload their bytes/links at run time, so one row covers
-- repeated enqueues. Startup recovery re-enqueues both.

-- +goose Up
CREATE TABLE artist_imports (
  artist_id TEXT PRIMARY KEY CHECK (length(artist_id) = 36) REFERENCES artists (id) ON DELETE CASCADE,
  state TEXT NOT NULL CHECK (state IN ('queued', 'running', 'done', 'failed')),
  queued_at TEXT,
  started_at TEXT,
  finished_at TEXT,
  error TEXT NOT NULL DEFAULT ''
);

CREATE TABLE pending_release_tasks (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  release_id TEXT NOT NULL CHECK (length(release_id) = 36) REFERENCES releases (id) ON DELETE CASCADE,
  kind TEXT NOT NULL CHECK (kind IN ('embed', 'clear', 'relink')),
  queued_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE (release_id, kind)
);

-- +goose Down
DROP TABLE IF EXISTS pending_release_tasks;
DROP TABLE IF EXISTS artist_imports;
