-- 0002_cover_art.sql — Per-release cover art metadata.
-- Bytes live on disk at <db-dir>/covers/<release-mbid>.<ext>; this table is
-- the truth for what is cached (source/mime/size/fetched_at).

-- +goose Up
CREATE TABLE release_covers (
  release_id TEXT PRIMARY KEY CHECK (length(release_id) = 36) REFERENCES releases (id) ON DELETE CASCADE,
  source TEXT NOT NULL CHECK (source IN ('coverartarchive', 'deezer', 'upload')),
  source_url TEXT,
  mime TEXT NOT NULL CHECK (mime IN ('image/jpeg', 'image/png', 'image/webp')),
  size_bytes INTEGER NOT NULL CHECK (size_bytes >= 0),
  fetched_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- +goose Down
DROP TABLE IF EXISTS release_covers;
