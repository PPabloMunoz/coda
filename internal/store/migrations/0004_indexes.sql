-- 0004_indexes.sql — Query-supporting indexes.

-- +goose Up
-- Suggestion tier 2 resolves unlinked files by their recording MBID
-- (store.suggest.go: t.recording_id = ?) — without this index every
-- suggestion request scans tracks.
CREATE INDEX idx_tracks_recording ON tracks (recording_id);

-- The scan prune (root_id = ? AND scanned_at < ?) and the known-files
-- reload (root_id IN (...)) both want a root-scoped index that carries
-- scanned_at.
CREATE INDEX idx_local_files_root_scanned ON local_files (root_id, scanned_at);

-- +goose Down
DROP INDEX IF EXISTS idx_local_files_root_scanned;
DROP INDEX IF EXISTS idx_tracks_recording;
