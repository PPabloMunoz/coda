-- 0003_artist_covers.sql — Per-artist display art (web UI only, never
-- embedded into files). Bytes live on disk at
-- <db-dir>/covers/artist-<mbid>.<ext>; this table is the truth for what is
-- cached (source/mime/size/fetched_at).

-- +goose Up
CREATE TABLE artist_covers (
  artist_id TEXT PRIMARY KEY CHECK (length(artist_id) = 36) REFERENCES artists (id) ON DELETE CASCADE,
  source TEXT NOT NULL CHECK (source IN ('deezer', 'wikimedia', 'upload')),
  source_url TEXT,
  mime TEXT NOT NULL CHECK (mime IN ('image/jpeg', 'image/png', 'image/webp')),
  size_bytes INTEGER NOT NULL CHECK (size_bytes >= 0),
  fetched_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- +goose Down
DROP TABLE IF EXISTS artist_covers;
