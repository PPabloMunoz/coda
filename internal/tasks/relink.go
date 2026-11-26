package tasks

import (
	"context"

	apperrors "github.com/ppablomunoz/coda/internal/errors"
	"github.com/ppablomunoz/coda/internal/utils"
)

// relinkRelease rewrites tags, cover, and paths for every file linked to a
// release after a release switch. The DB link is already remapped
// synchronously in ReplaceRelease, so this only heals on-disk state.
// Best-effort: per-file failures only log, occupied destinations are
// skipped and retried by the next scan, and a missing release or no linked
// files is a clean no-op.
func (r *Runner) relinkRelease(ctx context.Context, releaseID string) {
	if err := utils.ValidateMBID(releaseID); err != nil {
		r.log.Warnf("relink release %q: invalid id", releaseID)
		return
	}

	files, err := r.st.ListFilesByRelease(ctx, releaseID)
	if err != nil {
		r.log.Warnf("relink release %q list files: %v", releaseID, err)
		return
	}
	if len(files) == 0 {
		return
	}

	relinked, moved, skipped, failed := 0, 0, 0, 0
	for _, f := range files {
		select {
		case <-ctx.Done():
			r.log.Warnf("relink release %q: stopped early (%d relinked, %d moved, %d skipped, %d failed)",
				releaseID, relinked, moved, skipped, failed)
			return
		default:
		}
		if f.TrackID == nil {
			continue
		}
		linked, didMove, err := linkFile(ctx, r.st, r.coverDir, f.ID, *f.TrackID, true)
		if err != nil {
			if apperrors.Is(err, apperrors.ErrAlreadyExists) {
				skipped++
				r.log.Warnf("relink release %q file=%d: destination occupied, skipped", releaseID, f.ID)
				continue
			}
			if apperrors.Is(err, apperrors.ErrNotFound) {
				continue
			}
			failed++
			r.log.Warnf("relink release %q file=%d: %v", releaseID, f.ID, err)
			continue
		}
		if !linked {
			continue
		}
		relinked++
		if didMove {
			moved++
		}
	}
	r.log.Infof("relink release %q done: %d relinked, %d moved, %d skipped, %d failed",
		releaseID, relinked, moved, skipped, failed)
}
