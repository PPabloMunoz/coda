// Package tags reads audio file metadata and writes link tags (title, album,
// artists, track/disc, date, MBIDs, release type/status). Only LinkKeys are
// written or cleared, plus the embedded front cover on link and clear.
//
// Note: scans themselves do modify files through the auto-link pass
// (internal/tasks/autolink.go), which rewrites tags and moves files whose
// on-disk MBReleaseTrackID resolves to a known track — the same write path
// as manual link.
package tags
