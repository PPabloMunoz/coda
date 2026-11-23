package tasks

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ppablomunoz/coda/internal/config"
	apperrors "github.com/ppablomunoz/coda/internal/errors"
	"github.com/ppablomunoz/coda/internal/library"
	"github.com/ppablomunoz/coda/internal/store"
	"github.com/ppablomunoz/coda/internal/tags"
	"github.com/ppablomunoz/coda/internal/utils"
)

// AutoLinkStats tallies the scan auto-link pass. SkippedConflict counts files
// left untouched because their canonical destination was already occupied
// (filesystem or DB); the next scan retries them, so they are reported, not
// errors.
type AutoLinkStats struct {
	Checked         int
	Linked          int
	Moved           int
	SkippedConflict int
}

// LibraryTargetPath builds the on-disk destination for a linked track under
// root. The artist folder uses the first release-group artist only; tags
// still carry the full credits. Filesystem work stays in the caller; this is
// pure.
func LibraryTargetPath(root string, info *tags.LinkInfo, ext string) (string, error) {
	artist := info.FolderArtist
	if strings.TrimSpace(artist) == "" {
		artist = info.AlbumArtistDisplay
	}
	if strings.TrimSpace(artist) == "" {
		artist = info.ArtistDisplay
	}
	return library.Target{
		Root:       root,
		Artist:     artist,
		Album:      info.Album,
		Title:      info.Title,
		Disc:       info.DiscNumber,
		Track:      info.TrackNumber,
		TotalDiscs: info.DiscTotal,
		Ext:        ext,
	}.Path()
}

// EnsureMoveDestination creates the destination parent dir and reports
// whether renaming is safe. A pre-existing dst is ErrAlreadyExists so
// callers fail before writing tags.
func EnsureMoveDestination(op string, fileID int64, trackID, src, dst string) error {
	if dst == src {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("%s file=%d track=%q mkdir -> %w", op, fileID, trackID, err),
		)
	}
	if _, err := os.Lstat(dst); err == nil {
		return apperrors.ErrAlreadyExists
	} else if !os.IsNotExist(err) {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("%s file=%d track=%q stat destination -> %w", op, fileID, trackID, err),
		)
	}
	return nil
}

// EmbedCover stamps the cached release art into the audio file as its front
// cover. No cached art is a no-op (scan never blocks on the network); a
// present-but-unreadable cache surfaces as an error like any other link step.
func EmbedCover(ctx context.Context, st *store.Store, coverDir, filePath, releaseID string) error {
	row, err := st.GetCover(ctx, releaseID)
	if err != nil {
		if apperrors.Is(err, apperrors.ErrNotFound) {
			return nil
		}
		return err
	}
	path, err := config.CoverPath(coverDir, releaseID, row.MIME)
	if err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("cover path: %w", err),
		)
	}
	img, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Stale row; drop it so the next GET refetches, link goes on.
			_ = st.DeleteCover(ctx, releaseID)
			return nil
		}
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("cover file=%q read -> %w", releaseID, err),
		)
	}
	return tags.WriteCover(filePath, img)
}

// trackIDFromTags returns the file's release-track MBID when it is a usable
// MusicBrainz UUID. Anything else means "not auto-linkable": the scan leaves
// the file alone for the manual link wizard.
func trackIDFromTags(snap *tags.Snapshot) (string, bool) {
	if snap == nil {
		return "", false
	}
	id := strings.TrimSpace(snap.MBReleaseTrackID)
	if utils.ValidateMBID(id) != nil {
		return "", false
	}
	return id, true
}

// autoLinkMoves links exactly-identifiable files and moves them to their
// library path: for every tracked file whose on-disk MBReleaseTrackID is a
// valid MBID resolving to a known track, it rewrites tags + cover (same as
// manual link), renames into place, and records the link. Best-effort by
// design: per-file failures only log, occupied destinations are skipped and
// reported via SkippedConflict, and the scan never fails because of this
// pass.
func (r *Runner) autoLinkMoves(ctx context.Context) AutoLinkStats {
	var stats AutoLinkStats

	const batch = 200
	var all []store.LocalFile
	for offset := 0; ; offset += batch {
		select {
		case <-ctx.Done():
			return stats
		default:
		}
		files, total, err := r.st.ListFiles(ctx, store.FileListOptions{
			PageOptions: store.PageOptions{Limit: batch, Offset: offset},
		})
		if err != nil {
			r.log.Warnf("scan auto-link list files: %v", err)
			return stats
		}
		all = append(all, files...)
		if len(all) >= total || len(files) < batch {
			break
		}
	}

	roots := map[int64]string{}
	for _, f := range all {
		select {
		case <-ctx.Done():
			return stats
		default:
		}
		stats.Checked++

		snap, err := tags.Read(f.Path)
		if err != nil {
			r.log.Debugf("scan auto-link file=%d read tags: %v", f.ID, err)
			continue
		}
		trackID, ok := trackIDFromTags(snap)
		if !ok {
			continue
		}

		fullRelease, track, err := r.st.GetTrackFullRelease(ctx, trackID)
		if err != nil {
			if apperrors.Is(err, apperrors.ErrNotFound) {
				continue
			}
			r.log.Warnf("scan auto-link file=%d track=%q load release: %v", f.ID, trackID, err)
			continue
		}

		rootPath, ok := roots[f.RootID]
		if !ok {
			root, err := r.st.GetRoot(ctx, f.RootID)
			if err != nil {
				r.log.Warnf("scan auto-link file=%d load root: %v", f.ID, err)
				continue
			}
			rootPath = root.Path
			roots[f.RootID] = rootPath
		}

		info := LinkInfoFromStore(fullRelease, track)
		dst, err := LibraryTargetPath(rootPath, info, f.FormatHint)
		if err != nil {
			r.log.Debugf("scan auto-link file=%d track=%q build path: %v", f.ID, trackID, err)
			continue
		}
		if dst == f.Path && f.TrackID != nil && *f.TrackID == trackID {
			continue
		}

		if err := EnsureMoveDestination("scan", f.ID, trackID, f.Path, dst); err != nil {
			if apperrors.Is(err, apperrors.ErrAlreadyExists) {
				stats.SkippedConflict++
				r.log.Warnf("scan auto-link file=%d track=%q skipped: destination occupied (%q)", f.ID, trackID, dst)
				continue
			}
			r.log.Warnf("scan auto-link file=%d track=%q destination: %v", f.ID, trackID, err)
			continue
		}

		if err := tags.WriteTags(f.Path, tags.ToMap(tags.SnapshotFromLink(info))); err != nil {
			r.log.Warnf("scan auto-link file=%d track=%q write tags: %v", f.ID, trackID, err)
			continue
		}
		if err := EmbedCover(ctx, r.st, r.coverDir, f.Path, fullRelease.ID); err != nil {
			r.log.Warnf("scan auto-link file=%d track=%q write cover: %v", f.ID, trackID, err)
			continue
		}

		src := f.Path
		moved := dst != src
		if moved {
			if err := os.Rename(src, dst); err != nil {
				r.log.Warnf("scan auto-link file=%d track=%q move: %v", f.ID, trackID, err)
				continue
			}
		}

		fi, err := os.Stat(dst)
		if err != nil {
			// Tags were written and the file may have moved; the DB still
			// points at src. The next scan heals this to unlinked, never
			// to a wrong link.
			r.log.Errorf("scan auto-link file=%d track=%q stat: %v", f.ID, trackID, err)
			continue
		}

		if err := r.st.Link(ctx, f.ID, trackID, dst, fi.Size(), fi.ModTime().UnixNano()); err != nil {
			if moved {
				if rbErr := os.Rename(dst, src); rbErr != nil {
					r.log.Errorf("scan auto-link file=%d track=%q rollback failed: %v", f.ID, trackID, rbErr)
				}
			}
			if apperrors.Is(err, apperrors.ErrAlreadyExists) {
				stats.SkippedConflict++
				r.log.Warnf("scan auto-link file=%d track=%q skipped: destination tracked (%q)", f.ID, trackID, dst)
				continue
			}
			r.log.Warnf("scan auto-link file=%d track=%q save: %v", f.ID, trackID, err)
			continue
		}

		stats.Linked++
		if moved {
			stats.Moved++
		}
		r.log.Infof("scan auto-link file=%d track=%q moved=%t", f.ID, trackID, moved)
	}

	return stats
}
