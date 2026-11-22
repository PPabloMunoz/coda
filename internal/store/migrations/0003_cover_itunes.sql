-- 0003_cover_itunes.sql — Allow 'itunes' as a release cover source.
-- SQLite cannot alter a CHECK in place, so the table is rebuilt and rows
-- are copied over. Nothing references release_covers, so the swap is safe.

-- +goose Up
CREATE TABLE release_covers_new (
  release_id TEXT PRIMARY KEY CHECK (length(release_id) = 36) REFERENCES releases (id) ON DELETE CASCADE,
  source TEXT NOT NULL CHECK (source IN ('coverartarchive', 'deezer', 'itunes', 'upload')),
  source_url TEXT,
  mime TEXT NOT NULL CHECK (mime IN ('image/jpeg', 'image/png', 'image/webp')),
  size_bytes INTEGER NOT NULL CHECK (size_bytes >= 0),
  fetched_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO release_covers_new
  (release_id, source, source_url, mime, size_bytes, fetched_at)
  SELECT release_id, source, source_url, mime, size_bytes, fetched_at
  FROM release_covers;

DROP TABLE release_covers;

ALTER TABLE release_covers_new RENAME TO release_covers;

-- +goose Down
DELETE FROM release_covers WHERE source = 'itunes';

CREATE TABLE release_covers_old (
  release_id TEXT PRIMARY KEY CHECK (length(release_id) = 36) REFERENCES releases (id) ON DELETE CASCADE,
  source TEXT NOT NULL CHECK (source IN ('coverartarchive', 'deezer', 'upload')),
  source_url TEXT,
  mime TEXT NOT NULL CHECK (mime IN ('image/jpeg', 'image/png', 'image/webp')),
  size_bytes INTEGER NOT NULL CHECK (size_bytes >= 0),
  fetched_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO release_covers_old
  (release_id, source, source_url, mime, size_bytes, fetched_at)
  SELECT release_id, source, source_url, mime, size_bytes, fetched_at
  FROM release_covers;

DROP TABLE release_covers;

ALTER TABLE release_covers_old RENAME TO release_covers;
