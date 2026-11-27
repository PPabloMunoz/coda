package tasks

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	apperrors "github.com/ppablomunoz/coda/internal/errors"
	"github.com/ppablomunoz/coda/internal/store"
	"github.com/ppablomunoz/coda/internal/tags"
	"github.com/ppablomunoz/coda/internal/utils"
)

// fileOpLocks serializes link/refresh/unlink work per local file row. The
// scan auto-link pass and the HTTP handlers can target the same file
// concurrently; holding one mutex across move + tag write + cover embed +
// DB link makes the whole pipeline mutually exclusive per file. Entries are
// bounded by the number of distinct files ever linked.
var fileOpLocks sync.Map // int64 -> *sync.Mutex

// dstOpLocks serializes moves per canonical destination path. Two different
// files can resolve to the same library target (same track, same name); the
// atomic no-replace move already prevents overwrites, but holding the
// destination mutex keeps the loser's failure deterministic and keeps tag
// writes to a colliding path serialized. Entries are bounded by the number
// of distinct destinations ever targeted.
var dstOpLocks sync.Map // cleaned path -> *sync.Mutex

// moveLog reports best-effort ownership preservation failures. The move
// helpers have no handler to return warnings through, so a failed chown
// only warns here and the move still commits.
var moveLog = utils.New()

// preserveOwnership copies the original owner onto the move destination.
// It is a variable so tests can stub the platform helper to simulate a
// chown failure without changing file ownership.
var preserveOwnership = copyOwnership

func fileOpLock(id int64) *sync.Mutex {
	m, _ := fileOpLocks.LoadOrStore(id, &sync.Mutex{})
	return m.(*sync.Mutex)
}

func dstOpLock(dst string) *sync.Mutex {
	key := filepath.Clean(dst)
	m, _ := dstOpLocks.LoadOrStore(key, &sync.Mutex{})
	return m.(*sync.Mutex)
}

// LockFileOp acquires the per-file operation lock for id. The caller must
// call the returned func to release it. Unlink holds this across
// clear-tags + clear-cover + DB unlink so a concurrent link/refresh for the
// same file serializes instead of interleaving on a stale path.
func LockFileOp(id int64) func() {
	mu := fileOpLock(id)
	mu.Lock()
	return mu.Unlock
}

// LinkFile runs the shared link/move pipeline for one file: it resolves the
// canonical library destination for trackID, moves the file there with
// no-replace semantics, rewrites tags, embeds the cached cover, and records
// the DB link. The move happens before any tag mutation so an occupied
// destination, an unsupported move, or a cross-device failure leaves the
// source file untouched.
//
// It returns linked=false (nil error) when the file is already at its
// canonical path with the same track linked: an idempotent no-op that
// performs no writes. Otherwise linked=true and moved reports whether the
// file was relocated. An occupied destination (filesystem or DB) returns
// ErrAlreadyExists so callers answer 409 and leave the source untouched.
func LinkFile(ctx context.Context, st *store.Store, coverDir string, fileID int64, trackID string) (linked, moved bool, err error) {
	return linkFile(ctx, st, coverDir, fileID, trackID, false)
}

// linkFile is LinkFile with a force flag for the post-switch relink pass.
// force=true skips the already-current no-op so files whose DB link was
// remapped to a new track ID still get their tags, cover, and path
// rewritten even when the canonical path did not change.
func linkFile(ctx context.Context, st *store.Store, coverDir string, fileID int64, trackID string, force bool) (linked, moved bool, err error) {
	fileMu := fileOpLock(fileID)
	fileMu.Lock()
	defer fileMu.Unlock()

	if err := utils.ValidateMBID(trackID); err != nil {
		return false, false, apperrors.ErrInvalidID
	}

	localFile, err := st.GetFile(ctx, fileID, false)
	if err != nil {
		return false, false, fmt.Errorf("link file=%d load: %w", fileID, err)
	}

	fullRelease, track, err := st.GetTrackFullRelease(ctx, trackID)
	if err != nil {
		return false, false, fmt.Errorf("link file=%d track=%q load release: %w", fileID, trackID, err)
	}
	info := LinkInfoFromStore(fullRelease, track)

	root, err := st.GetRoot(ctx, localFile.RootID)
	if err != nil {
		return false, false, fmt.Errorf("link file=%d load root: %w", fileID, err)
	}

	dst, err := LibraryTargetPath(root.Path, info, localFile.FormatHint)
	if err != nil {
		return false, false, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("link file=%d track=%q build path: %w", fileID, trackID, err),
		)
	}
	src := localFile.Path

	dstMu := dstOpLock(dst)
	dstMu.Lock()
	defer dstMu.Unlock()

	if !force && filepath.Clean(dst) == filepath.Clean(src) &&
		localFile.TrackID != nil && *localFile.TrackID == trackID {
		return false, false, nil
	}

	moved = filepath.Clean(dst) != filepath.Clean(src)
	if moved {
		if err := moveFileNoReplace("link", fileID, trackID, src, dst); err != nil {
			return false, false, err
		}
	}
	finalPath := dst

	if err := tags.WriteTags(finalPath, tags.ToMap(tags.SnapshotFromLink(info))); err != nil {
		if moved {
			if rbErr := moveFileNoReplace("link-rollback", fileID, trackID, dst, src); rbErr != nil {
				return false, false, apperrors.Wrap(
					apperrors.ErrInternal,
					fmt.Errorf("link file=%d track=%q write tags: %v; rollback %q -> %q: %v",
						fileID, trackID, err, dst, src, rbErr),
				)
			}
		}
		if _, ok := apperrors.Get(err); ok {
			return false, false, fmt.Errorf("link file=%d track=%q write tags: %w", fileID, trackID, err)
		}
		return false, false, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("link file=%d track=%q write tags: %w", fileID, trackID, err),
		)
	}
	if err := EmbedCover(ctx, st, coverDir, finalPath, fullRelease.ID); err != nil {
		if moved {
			if rbErr := moveFileNoReplace("link-rollback", fileID, trackID, dst, src); rbErr != nil {
				return false, false, apperrors.Wrap(
					apperrors.ErrInternal,
					fmt.Errorf("link file=%d track=%q write cover: %v; rollback %q -> %q: %v",
						fileID, trackID, err, dst, src, rbErr),
				)
			}
		}
		if _, ok := apperrors.Get(err); ok {
			return false, false, fmt.Errorf("link file=%d track=%q write cover: %w", fileID, trackID, err)
		}
		return false, false, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("link file=%d track=%q write cover: %w", fileID, trackID, err),
		)
	}

	fi, err := os.Stat(finalPath)
	if err != nil {
		// Tags were written and the file may have moved; the DB still
		// points at src. The next scan heals this to unlinked, never
		// to a wrong link. No rollback: the file is gone or unreadable.
		if _, ok := apperrors.Get(err); ok {
			return false, false, fmt.Errorf("link file=%d track=%q stat: %w", fileID, trackID, err)
		}
		return false, false, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("link file=%d track=%q stat: %w", fileID, trackID, err),
		)
	}

	if err := st.Link(ctx, fileID, trackID, dst, fi.Size(), fi.ModTime().UnixNano()); err != nil {
		if moved {
			if rbErr := moveFileNoReplace("link-rollback", fileID, trackID, dst, src); rbErr != nil {
				return false, false, apperrors.Wrap(
					apperrors.ErrInternal,
					fmt.Errorf("link file=%d track=%q save: %v; rollback %q -> %q: %v",
						fileID, trackID, err, dst, src, rbErr),
				)
			}
		}
		if _, ok := apperrors.Get(err); ok {
			return false, false, fmt.Errorf("link file=%d track=%q save: %w", fileID, trackID, err)
		}
		return false, false, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("link file=%d track=%q save: %w", fileID, trackID, err),
		)
	}

	return true, moved, nil
}

// moveFileNoReplace relocates src to dst without ever replacing an existing
// destination. A same-file move is a no-op. Otherwise it creates the
// destination parent, hard-links src to dst and removes src, falling back to
// an O_EXCL copy (same filesystem or cross-device) when hard links are
// unsupported. An existing dst — at link time or at O_EXCL create time, so a
// concurrent mover racing us still loses cleanly — returns ErrAlreadyExists
// with the source untouched.
func moveFileNoReplace(op string, fileID int64, trackID, src, dst string) error {
	if filepath.Clean(src) == filepath.Clean(dst) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("%s file=%d track=%q mkdir: %w", op, fileID, trackID, err),
		)
	}
	if err := os.Link(src, dst); err == nil {
		if err := os.Remove(src); err != nil {
			_ = os.Remove(dst)
			return apperrors.Wrap(
				apperrors.ErrInternal,
				fmt.Errorf("%s file=%d track=%q remove source: %w", op, fileID, trackID, err),
			)
		}
		syncDir(filepath.Dir(dst))
		syncDir(filepath.Dir(src))
		return nil
	} else if os.IsExist(err) {
		return apperrors.Wrap(
			apperrors.ErrAlreadyExists,
			fmt.Errorf("%s file=%d track=%q destination occupied (%q): %w", op, fileID, trackID, dst, err),
		)
	} else if !os.IsNotExist(err) {
		// Link failed for a reason other than a missing source (cross-device,
		// unsupported filesystem, permission): fall back to an O_EXCL copy.
		// A missing source surfaces from the copy's open with the same
		// internal code below, so only EEXIST is special-cased above.
		if copyErr := copyFileNoReplace(op, fileID, trackID, src, dst); copyErr != nil {
			return copyErr
		}
		return nil
	}
	// os.Link reported the source missing; run the copy path anyway so the
	// error names the source open instead of leaking link(2) errno text.
	return copyFileNoReplace(op, fileID, trackID, src, dst)
}

// copyFileNoReplace copies src to a dst created with O_CREATE|O_EXCL, then
// removes src. An existing dst returns ErrAlreadyExists; the source is only
// removed after the copy is fsynced and closed, so a failure never loses a
// file. The copy keeps the source mode and, on Unix, the source owner
// (best effort); xattrs/ACLs are not preserved.
func copyFileNoReplace(op string, fileID int64, trackID, src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("%s file=%d track=%q mkdir: %w", op, fileID, trackID, err),
		)
	}
	in, err := os.Open(src)
	if err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("%s file=%d track=%q open source: %w", op, fileID, trackID, err),
		)
	}
	defer func() { _ = in.Close() }()
	srcInfo, err := in.Stat()
	if err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("%s file=%d track=%q stat source: %w", op, fileID, trackID, err),
		)
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return apperrors.Wrap(
				apperrors.ErrAlreadyExists,
				fmt.Errorf("%s file=%d track=%q destination occupied (%q): %w", op, fileID, trackID, dst, err),
			)
		}
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("%s file=%d track=%q create destination: %w", op, fileID, trackID, err),
		)
	}
	_, copyErr := io.Copy(out, in)
	syncErr := out.Sync()
	closeErr := out.Close()
	if copyErr != nil || syncErr != nil || closeErr != nil {
		_ = os.Remove(dst)
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("%s file=%d track=%q copy: copy=%v sync=%v close=%v",
				op, fileID, trackID, copyErr, syncErr, closeErr),
		)
	}
	if err := os.Chmod(dst, srcInfo.Mode().Perm()); err != nil {
		_ = os.Remove(dst)
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("%s file=%d track=%q chmod destination: %w", op, fileID, trackID, err),
		)
	}
	// Best effort: keep the original owner on the copy so a cross-device
	// or no-hard-link move does not silently hand the file to the Coda
	// user. A failure only warns; the move still commits with the copy's
	// owner. xattrs/ACLs are not preserved; the new file inherits the
	// destination directory defaults.
	if err := preserveOwnership(dst, srcInfo); err != nil {
		moveLog.Warnf("%s file=%d track=%q preserve ownership for %q: %v", op, fileID, trackID, dst, err)
	}
	if err := os.Remove(src); err != nil {
		_ = os.Remove(dst)
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("%s file=%d track=%q remove source: %w", op, fileID, trackID, err),
		)
	}
	syncDir(filepath.Dir(dst))
	syncDir(filepath.Dir(src))
	return nil
}

// syncDir flushes a directory entry after a move. Not every platform
// supports directory fsync; failures are deliberately ignored because the
// move itself already succeeded.
func syncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
}
