-- 0010_incremental_scan.sql — Incremental scan evidence and phase timings.
-- local_files gains tag evidence so a settled rescan skips tag reads:
-- mb_track_id is the raw MUSICBRAINZ_RELEASETRACKID from the file (NULL
-- until matched, even when the track is not imported yet); tags_read_at
-- records when tags were last read (NULL means never read / retry).
-- Linked rows are backfilled as already-read so the first scan after
-- upgrade does not re-read them; unlinked rows re-read once.
-- scan_runs gains per-phase timings and incremental counters so the
-- scan status shows where time went.

-- +goose Up
ALTER TABLE local_files ADD COLUMN mb_track_id TEXT CHECK (mb_track_id IS NULL OR length(mb_track_id) = 36);
ALTER TABLE local_files ADD COLUMN tags_read_at TEXT;

UPDATE local_files SET tags_read_at = scanned_at, mb_track_id = track_id WHERE track_id IS NOT NULL;

CREATE INDEX idx_local_files_mb_track ON local_files (mb_track_id);

ALTER TABLE scan_runs ADD COLUMN walk_ms INTEGER NOT NULL DEFAULT 0;
ALTER TABLE scan_runs ADD COLUMN sync_ms INTEGER NOT NULL DEFAULT 0;
ALTER TABLE scan_runs ADD COLUMN tag_read_ms INTEGER NOT NULL DEFAULT 0;
ALTER TABLE scan_runs ADD COLUMN autolink_ms INTEGER NOT NULL DEFAULT 0;
ALTER TABLE scan_runs ADD COLUMN tag_reads INTEGER NOT NULL DEFAULT 0;
ALTER TABLE scan_runs ADD COLUMN unchanged INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE scan_runs DROP COLUMN unchanged;
ALTER TABLE scan_runs DROP COLUMN tag_reads;
ALTER TABLE scan_runs DROP COLUMN autolink_ms;
ALTER TABLE scan_runs DROP COLUMN tag_read_ms;
ALTER TABLE scan_runs DROP COLUMN sync_ms;
ALTER TABLE scan_runs DROP COLUMN walk_ms;
DROP INDEX IF EXISTS idx_local_files_mb_track;
ALTER TABLE local_files DROP COLUMN tags_read_at;
ALTER TABLE local_files DROP COLUMN mb_track_id;
