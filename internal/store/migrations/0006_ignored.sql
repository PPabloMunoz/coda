-- 0006_ignored.sql — Durable user-deleted tombstones.
-- A normal delete just removes rows; a permanent delete also records the
-- MBID here so later discography imports never recreate it. `id` is the
-- primary key because MusicBrainz IDs are globally unique. `type`
-- distinguishes artists from release groups for listing and MusicBrainz
-- links. Restoring is removing the row; nothing auto-refreshes.

-- +goose Up
CREATE TABLE ignored (
  id TEXT PRIMARY KEY CHECK (length(id) = 36),
  name TEXT NOT NULL,
  type TEXT NOT NULL CHECK (type IN ('artist', 'release_group')),
  deleted_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- +goose Down
DROP TABLE IF EXISTS ignored;
