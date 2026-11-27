-- 0011_rfc3339_timestamps.sql — Standardize stored timestamps on RFC3339 UTC.
-- Earlier migrations defaulted several columns to CURRENT_TIMESTAMP
-- (YYYY-MM-DD HH:MM:SS, no timezone), while newer code writes RFC3339
-- (YYYY-MM-DDTHH:MM:SSZ). The API documents RFC3339 and Safari cannot
-- reliably parse the space format, so normalize existing rows. New writes
-- are RFC3339 in code; column defaults are left untouched for
-- compatibility.

-- +goose Up
UPDATE roots SET created_at = strftime('%Y-%m-%dT%H:%M:%SZ', created_at)
  WHERE created_at IS NOT NULL AND instr(created_at, 'T') = 0;
UPDATE local_files SET scanned_at = strftime('%Y-%m-%dT%H:%M:%SZ', scanned_at)
  WHERE scanned_at IS NOT NULL AND instr(scanned_at, 'T') = 0;
UPDATE local_files SET tags_read_at = strftime('%Y-%m-%dT%H:%M:%SZ', tags_read_at)
  WHERE tags_read_at IS NOT NULL AND instr(tags_read_at, 'T') = 0;
UPDATE ignored SET deleted_at = strftime('%Y-%m-%dT%H:%M:%SZ', deleted_at)
  WHERE deleted_at IS NOT NULL AND instr(deleted_at, 'T') = 0;
UPDATE release_covers SET fetched_at = strftime('%Y-%m-%dT%H:%M:%SZ', fetched_at)
  WHERE fetched_at IS NOT NULL AND instr(fetched_at, 'T') = 0;
UPDATE artist_covers SET fetched_at = strftime('%Y-%m-%dT%H:%M:%SZ', fetched_at)
  WHERE fetched_at IS NOT NULL AND instr(fetched_at, 'T') = 0;
UPDATE release_cover_misses SET checked_at = strftime('%Y-%m-%dT%H:%M:%SZ', checked_at)
  WHERE checked_at IS NOT NULL AND instr(checked_at, 'T') = 0;
UPDATE artist_cover_misses SET checked_at = strftime('%Y-%m-%dT%H:%M:%SZ', checked_at)
  WHERE checked_at IS NOT NULL AND instr(checked_at, 'T') = 0;
UPDATE pending_release_tasks SET queued_at = strftime('%Y-%m-%dT%H:%M:%SZ', queued_at)
  WHERE queued_at IS NOT NULL AND instr(queued_at, 'T') = 0;
UPDATE artist_imports SET queued_at = strftime('%Y-%m-%dT%H:%M:%SZ', queued_at)
  WHERE queued_at IS NOT NULL AND instr(queued_at, 'T') = 0;
UPDATE artist_imports SET started_at = strftime('%Y-%m-%dT%H:%M:%SZ', started_at)
  WHERE started_at IS NOT NULL AND instr(started_at, 'T') = 0;
UPDATE artist_imports SET finished_at = strftime('%Y-%m-%dT%H:%M:%SZ', finished_at)
  WHERE finished_at IS NOT NULL AND instr(finished_at, 'T') = 0;
UPDATE scan_runs SET queued_at = strftime('%Y-%m-%dT%H:%M:%SZ', queued_at)
  WHERE queued_at IS NOT NULL AND instr(queued_at, 'T') = 0;
UPDATE scan_runs SET started_at = strftime('%Y-%m-%dT%H:%M:%SZ', started_at)
  WHERE started_at IS NOT NULL AND instr(started_at, 'T') = 0;
UPDATE scan_runs SET finished_at = strftime('%Y-%m-%dT%H:%M:%SZ', finished_at)
  WHERE finished_at IS NOT NULL AND instr(finished_at, 'T') = 0;

-- +goose Down
-- Timestamps stay RFC3339 on downgrade; no reverse conversion.
SELECT 1;
