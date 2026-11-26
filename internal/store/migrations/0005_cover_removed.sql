-- 0005_cover_removed.sql — User-removed cover tombstones.
-- Deleting a cached cover must also suppress the lazy provider refetch on
-- the next GET .../cover, or the art would come straight back. `removed`
-- marks that tombstone: getters treat the row as missing without fetching,
-- uploads and refreshes clear it (upsert writes removed=0, refresh drops
-- the row), and the background file jobs no-op on it.

-- +goose Up
ALTER TABLE release_covers ADD COLUMN removed INTEGER NOT NULL DEFAULT 0;
ALTER TABLE artist_covers ADD COLUMN removed INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE release_covers DROP COLUMN removed;
ALTER TABLE artist_covers DROP COLUMN removed;
