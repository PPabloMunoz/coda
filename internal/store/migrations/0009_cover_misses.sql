-- 0009_cover_misses.sql — Negative-cache cover-art misses.
-- A provider miss (no art on CAA/Deezer/iTunes, or Deezer/Wikimedia) is
-- recorded here so later GETs stay 404 until the retry time instead of
-- hitting providers again on every page load. Tombstones in release_covers
-- / artist_covers stay separate and always win: user deletion is never
-- undone by a miss. Uploads, refreshes, removals, and stale-row deletes
-- clear the matching miss row.

-- +goose Up
CREATE TABLE release_cover_misses (
  release_id TEXT PRIMARY KEY CHECK (length(release_id) = 36) REFERENCES releases (id) ON DELETE CASCADE,
  checked_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  misses INTEGER NOT NULL DEFAULT 1 CHECK (misses >= 1),
  retry_after INTEGER NOT NULL
);

CREATE TABLE artist_cover_misses (
  artist_id TEXT PRIMARY KEY CHECK (length(artist_id) = 36) REFERENCES artists (id) ON DELETE CASCADE,
  checked_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  misses INTEGER NOT NULL DEFAULT 1 CHECK (misses >= 1),
  retry_after INTEGER NOT NULL
);

-- +goose Down
DROP TABLE IF EXISTS artist_cover_misses;
DROP TABLE IF EXISTS release_cover_misses;
