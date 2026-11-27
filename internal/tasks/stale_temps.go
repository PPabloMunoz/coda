package tasks

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ppablomunoz/coda/internal/tags"
)

// staleTempAge is how old a leftover ".coda-tag-*" temp copy must be
// before the scan removes it. Tag writes finish in seconds, so 24h
// keeps in-flight writes from concurrent link/refresh handlers safe
// while eventually reclaiming crash leftovers.
const staleTempAge = 24 * time.Hour

// cleanupStaleTemps removes leftover ".coda-tag-*" temp copies older than
// staleTempAge. It runs before the library walk so a temp indexed by an
// older version is gone before SyncLocalFiles prunes its bogus row in
// the same scan. Best effort: per-file failures only log.
//
// The walk and the remove are both scoped to the root via os.Root so a
// symlink swapped mid-walk cannot redirect the removal outside the
// library (gosec G122).
func (r *Runner) cleanupStaleTemps(ctx context.Context) {
	for _, root := range r.st.RootPaths() {
		ro, err := os.OpenRoot(root)
		if err != nil {
			r.log.Warnf("scan stale-temp cleanup root=%q: %v", root, err)
			continue
		}
		err = fs.WalkDir(ro.FS(), ".", func(path string, d fs.DirEntry, err error) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}

			if err != nil {
				return nil // unreadable entry: Scan reports it, cleanup ignores it
			}
			if d.IsDir() {
				if path != "." && strings.HasPrefix(d.Name(), ".") {
					return filepath.SkipDir
				}
				return nil
			}
			if d.Type()&fs.ModeSymlink != 0 {
				return nil
			}
			if !tags.IsTempFile(d.Name()) {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil // disappeared mid-walk, nothing to do
			}
			if !info.Mode().IsRegular() {
				return nil
			}
			if time.Since(info.ModTime()) < staleTempAge {
				return nil // possibly in-flight, leave it
			}
			if err := ro.Remove(path); err != nil {
				if !os.IsNotExist(err) {
					r.log.Warnf("scan removing stale tag temp %q: %v", filepath.Join(root, path), err)
				}
				return nil
			}
			r.log.Infof("scan removed stale tag temp %q", filepath.Join(root, path))
			return nil
		})
		_ = ro.Close()
		if err != nil {
			r.log.Warnf("scan stale-temp cleanup root=%q: %v", root, err)
			return
		}
	}
}
