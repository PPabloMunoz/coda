package tasks

import (
	"context"
	"fmt"
	"os"
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

// EmbedCover stamps the cached release art into the audio file as its front
// cover. No cached art — or a user-removed cover — is a no-op (scan never
// blocks on the network); a present-but-unreadable cache surfaces as an
// error like any other link step.
func EmbedCover(ctx context.Context, st *store.Store, coverDir, filePath, releaseID string) error {
	row, err := st.GetCover(ctx, releaseID)
	if err != nil {
		if apperrors.Is(err, apperrors.ErrNotFound) {
			return nil
		}
		return err
	}
	if row.Removed {
		return nil
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
			// Only drop the row we checked: a concurrent upload, refresh,
			// or delete may have replaced it or recorded a tombstone.
			if cur, gerr := st.GetCover(ctx, releaseID); gerr == nil &&
				!cur.Removed && cur.MIME == row.MIME &&
				cur.SizeBytes == row.SizeBytes && cur.FetchedAt == row.FetchedAt {
				_ = st.DeleteCover(ctx, releaseID)
			}
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
// library path: for every file whose stored tag MBID resolves to a known
// track but whose link differs, it rewrites tags + cover (same as manual
// link), renames into place, and records the link. Candidates come from a
// paged DB query on stored tag evidence, so a settled rescan reads no tags
// here (tag parses happen once in the sync). Best-effort by design:
// per-file failures only log, occupied destinations are skipped and
// reported via SkippedConflict, and the scan never fails because of this
// pass.
func (r *Runner) autoLinkMoves(ctx context.Context) AutoLinkStats {
	var stats AutoLinkStats

	const batch = 200
	var afterID int64
	for {
		select {
		case <-ctx.Done():
			return stats
		default:
		}
		candidates, err := r.st.ListAutoLinkCandidates(ctx, afterID, batch)
		if err != nil {
			r.log.Warnf("scan auto-link list candidates: %s", apperrors.LogDetail(err))
			return stats
		}
		if len(candidates) == 0 {
			break
		}
		for _, c := range candidates {
			select {
			case <-ctx.Done():
				return stats
			default:
			}
			afterID = c.ID
			stats.Checked++

			// The shared pipeline serializes per file and per destination,
			// moves with no-replace semantics before writing tags, and maps an
			// occupied destination to ErrAlreadyExists.
			linked, moved, err := LinkFile(ctx, r.st, r.coverDir, c.ID, c.MbTrackID)
			if err != nil {
				if apperrors.Is(err, apperrors.ErrAlreadyExists) {
					stats.SkippedConflict++
					r.log.Warnf("scan auto-link file=%d track=%q skipped: destination occupied", c.ID, c.MbTrackID)
					continue
				}
				if apperrors.Is(err, apperrors.ErrNotFound) {
					continue
				}
				r.log.Warnf("scan auto-link file=%d track=%q: %s", c.ID, c.MbTrackID, apperrors.LogDetail(err))
				continue
			}
			if !linked {
				continue
			}

			stats.Linked++
			if moved {
				stats.Moved++
			}
			r.log.Infof("scan auto-link file=%d track=%q moved=%t", c.ID, c.MbTrackID, moved)
		}
		if len(candidates) < batch {
			break
		}
	}

	return stats
}
