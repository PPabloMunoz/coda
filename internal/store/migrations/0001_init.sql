-- 0001_init.sql — Coda initial SQLite schema.

-- +goose Up
CREATE TABLE roots (
  id INTEGER PRIMARY KEY,
  path TEXT NOT NULL UNIQUE,
  active INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0, 1)),
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE artists (
  id TEXT PRIMARY KEY CHECK (length(id) = 36),
  name TEXT NOT NULL,
  sort_name TEXT,
  disambiguation TEXT,
  active INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0, 1)),
  in_library INTEGER NOT NULL DEFAULT 0 CHECK (in_library IN (0, 1))
);

CREATE TABLE artist_credits (
  id INTEGER PRIMARY KEY
);

CREATE TABLE artist_credit_items (
  artist_credit_id INTEGER NOT NULL REFERENCES artist_credits (id) ON DELETE CASCADE,
  position INTEGER NOT NULL CHECK (position >= 0),
  artist_id TEXT NOT NULL REFERENCES artists (id) ON DELETE RESTRICT,
  credited_name TEXT NOT NULL,
  join_phrase TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (artist_credit_id, position)
);

CREATE TABLE release_groups (
  id TEXT PRIMARY KEY CHECK (length(id) = 36),
  title TEXT NOT NULL,
  type TEXT NOT NULL DEFAULT 'other' CHECK (type IN ('album', 'ep', 'single', 'other')),
  artist_credit_id INTEGER REFERENCES artist_credits (id) ON DELETE SET NULL
);

CREATE TABLE releases (
  id TEXT PRIMARY KEY CHECK (length(id) = 36),
  release_group_id TEXT NOT NULL UNIQUE REFERENCES release_groups (id) ON DELETE CASCADE,
  title TEXT NOT NULL,
  artist_credit_id INTEGER REFERENCES artist_credits (id) ON DELETE SET NULL,
  date TEXT,
  status TEXT
);

CREATE TABLE tracks (
  id TEXT PRIMARY KEY CHECK (length(id) = 36),
  release_id TEXT NOT NULL REFERENCES releases (id) ON DELETE CASCADE,
  disc_number INTEGER NOT NULL DEFAULT 1 CHECK (disc_number >= 1),
  position INTEGER NOT NULL CHECK (position >= 1),
  number TEXT NOT NULL,
  title TEXT NOT NULL,
  artist_credit_id INTEGER REFERENCES artist_credits (id) ON DELETE SET NULL,
  duration_ms INTEGER CHECK (duration_ms IS NULL OR duration_ms >= 0),
  recording_id TEXT CHECK (length(recording_id) = 36),
  UNIQUE (release_id, disc_number, position)
);

CREATE TABLE local_files (
  id INTEGER PRIMARY KEY,
  root_id INTEGER NOT NULL REFERENCES roots (id) ON DELETE CASCADE,
  path TEXT NOT NULL UNIQUE,
  size_bytes INTEGER NOT NULL CHECK (size_bytes >= 0),
  mtime_ns INTEGER NOT NULL,
  format_hint TEXT NOT NULL,
  scanned_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  track_id TEXT REFERENCES tracks (id) ON DELETE SET NULL
);

CREATE INDEX idx_artist_credit_items_artist ON artist_credit_items (artist_id);
CREATE INDEX idx_release_groups_credit ON release_groups (artist_credit_id);
CREATE INDEX idx_releases_credit ON releases (artist_credit_id);
CREATE INDEX idx_tracks_release ON tracks (release_id);
CREATE INDEX idx_tracks_credit ON tracks (artist_credit_id);
CREATE INDEX idx_local_files_root ON local_files (root_id);
CREATE INDEX idx_local_files_track ON local_files (track_id);

-- +goose Down
DROP INDEX IF EXISTS idx_local_files_track;
DROP INDEX IF EXISTS idx_local_files_root;
DROP INDEX IF EXISTS idx_tracks_credit;
DROP INDEX IF EXISTS idx_tracks_release;
DROP INDEX IF EXISTS idx_releases_credit;
DROP INDEX IF EXISTS idx_release_groups_credit;
DROP INDEX IF EXISTS idx_artist_credit_items_artist;
DROP TABLE IF EXISTS local_files;
DROP TABLE IF EXISTS tracks;
DROP TABLE IF EXISTS artist_credit_items;
DROP TABLE IF EXISTS releases;
DROP TABLE IF EXISTS release_groups;
DROP TABLE IF EXISTS artist_credits;
DROP TABLE IF EXISTS artists;
DROP TABLE IF EXISTS roots;
