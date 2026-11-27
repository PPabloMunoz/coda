package tasks

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ppablomunoz/coda/internal/config"
	apperrors "github.com/ppablomunoz/coda/internal/errors"
	"github.com/ppablomunoz/coda/internal/store"
	"github.com/ppablomunoz/coda/internal/utils"
)

// PruneOrphanCovers removes cached cover files with no live database row:
// releases or artists deleted since (artist deletes cascade rows but the
// files stay on disk, as does the weekly inactive-artist hard-delete),
// user-removed tombstones whose files survived, stale siblings from an
// earlier MIME (same release, different extension), and leftover
// ".coda-cover-*"/".coda-artist-cover-*" temp files older than
// staleTempAge. Files that do not look like cover cache entries
// (non-MBID names) are never touched. Each candidate is re-checked under
// its per-release/per-artist cover lock so a concurrent fetch, upload,
// delete, or refresh cannot lose a live file. Best-effort: per-file
// failures only log. Returns files removed.
func PruneOrphanCovers(
	ctx context.Context, st *store.Store, coverDir string, log *utils.Logger,
) int {
	if coverDir == "" {
		return 0
	}
	entries, err := os.ReadDir(coverDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		if log != nil {
			log.Warnf("cover GC dir %q: %v", coverDir, err)
		}
		return 0
	}
	removed := 0
	for _, e := range entries {
		select {
		case <-ctx.Done():
			return removed
		default:
		}
		if e.IsDir() {
			continue
		}
		name := e.Name()
		full := filepath.Join(coverDir, name)
		if strings.HasPrefix(name, ".coda-cover-") ||
			strings.HasPrefix(name, ".coda-artist-cover-") {
			if pruneStaleCoverTemp(full, e, log) {
				removed++
			}
			continue
		}
		if strings.HasPrefix(name, "artist-") {
			rest := strings.TrimPrefix(name, "artist-")
			ext := filepath.Ext(rest)
			id := strings.TrimSuffix(rest, ext)
			if utils.ValidateMBID(id) != nil {
				continue
			}
			if pruneOrphanArtistCover(ctx, st, full, coverDir, id, ext, log) {
				removed++
			}
			continue
		}
		ext := filepath.Ext(name)
		id := strings.TrimSuffix(name, ext)
		if utils.ValidateMBID(id) != nil {
			continue
		}
		if pruneOrphanReleaseCover(ctx, st, full, id, ext, log) {
			removed++
		}
	}
	return removed
}

// pruneStaleCoverTemp removes one cover temp copy older than staleTempAge.
// Fresh temps may belong to an in-flight fetch or upload and are left
// alone. Reports whether the file was removed.
func pruneStaleCoverTemp(full string, e os.DirEntry, log *utils.Logger) bool {
	info, err := e.Info()
	if err != nil {
		return false
	}
	if !info.Mode().IsRegular() {
		return false
	}
	if time.Since(info.ModTime()) < staleTempAge {
		return false
	}
	if err := os.Remove(full); err != nil {
		if !os.IsNotExist(err) && log != nil {
			log.Warnf("cover GC removing stale temp %q: %v", full, err)
		}
		return false
	}
	if log != nil {
		log.Infof("cover GC removed stale temp %q", full)
	}
	return true
}

// pruneOrphanReleaseCover removes the cached file for a release when its
// row is gone (deleted release/group) or tombstoned (user removal), and
// removes only the mismatched file when the row points at a different
// extension (stale sibling from an earlier MIME). Reports whether a file
// was removed.
func pruneOrphanReleaseCover(
	ctx context.Context, st *store.Store, full, releaseID, ext string, log *utils.Logger,
) bool {
	removed := false
	err := WithReleaseCoverLock(releaseID, func() error {
		row, err := st.GetCover(ctx, releaseID)
		if err != nil {
			if apperrors.Is(err, apperrors.ErrNotFound) {
				coverDir := filepath.Dir(full)
				removeCoverFile(coverDir, releaseID, log)
				removed = true
				return nil
			}
			return err
		}
		if row.Removed {
			coverDir := filepath.Dir(full)
			removeCoverFile(coverDir, releaseID, log)
			removed = true
			return nil
		}
		want, werr := config.ExtFromMIME(row.MIME)
		if werr != nil {
			return nil
		}
		if !strings.EqualFold(ext, "."+want) {
			if rerr := os.Remove(full); rerr != nil && !os.IsNotExist(rerr) {
				if log != nil {
					log.Warnf("cover GC removing %q: %v", full, rerr)
				}
				return nil
			}
			removed = true
		}
		return nil
	})
	if err != nil && log != nil {
		log.Warnf("cover GC release %q: %v", releaseID, err)
	}
	return removed
}

// pruneOrphanArtistCover mirrors pruneOrphanReleaseCover for artist photos.
func pruneOrphanArtistCover(
	ctx context.Context, st *store.Store, full, coverDir, artistID, ext string, log *utils.Logger,
) bool {
	removed := false
	err := WithArtistCoverLock(artistID, func() error {
		row, err := st.GetArtistCover(ctx, artistID)
		if err != nil {
			if apperrors.Is(err, apperrors.ErrNotFound) {
				removeArtistCoverFile(coverDir, artistID, log)
				removed = true
				return nil
			}
			return err
		}
		if row.Removed {
			removeArtistCoverFile(coverDir, artistID, log)
			removed = true
			return nil
		}
		want, werr := config.ExtFromMIME(row.MIME)
		if werr != nil {
			return nil
		}
		if !strings.EqualFold(ext, "."+want) {
			if rerr := os.Remove(full); rerr != nil && !os.IsNotExist(rerr) {
				if log != nil {
					log.Warnf("cover GC removing %q: %v", full, rerr)
				}
				return nil
			}
			removed = true
		}
		return nil
	})
	if err != nil && log != nil {
		log.Warnf("cover GC artist %q: %v", artistID, err)
	}
	return removed
}

// removeCoverFile deletes every cached file for a release (all
// extensions). The per-release lock is held by the caller.
func removeCoverFile(coverDir, releaseID string, log *utils.Logger) {
	matches, _ := filepath.Glob(filepath.Join(coverDir, releaseID+".*"))
	for _, m := range matches {
		if err := os.Remove(m); err != nil && !os.IsNotExist(err) && log != nil {
			log.Warnf("cover GC removing %q: %v", m, err)
		}
	}
}

// removeArtistCoverFile deletes every cached file for an artist (all
// extensions). The per-artist lock is held by the caller.
func removeArtistCoverFile(coverDir, artistID string, log *utils.Logger) {
	matches, _ := filepath.Glob(filepath.Join(coverDir, "artist-"+artistID+".*"))
	for _, m := range matches {
		if err := os.Remove(m); err != nil && !os.IsNotExist(err) && log != nil {
			log.Warnf("cover GC removing %q: %v", m, err)
		}
	}
}
