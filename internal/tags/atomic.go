package tags

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// fileLocks serializes mutating tag operations per cleaned path. The
// scan's auto-link pass and the HTTP handlers can target the same audio
// file concurrently, and taglib / id3v2 must never write one file from
// two goroutines at once. Entries are bounded by the number of distinct
// files ever written.
var fileLocks sync.Map // map[string]*sync.Mutex

// lockFor returns the shared mutex for path.
func lockFor(path string) *sync.Mutex {
	key := filepath.Clean(path)
	m, _ := fileLocks.LoadOrStore(key, &sync.Mutex{})
	return m.(*sync.Mutex)
}

// mutate applies write to a same-directory temporary copy of path and
// renames the copy over path, so a crash or error mid-write leaves the
// original file untouched. The rename is the single atomic commit point;
// everything before it only touches the temp file. Callers serialize on
// the per-path lock, making scan-time and handler-time writes mutually
// exclusive.
func mutate(path string, write func(tmp string) error) error {
	mu := lockFor(path)
	mu.Lock()
	defer mu.Unlock()

	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("tags: stat %q: %w", path, err)
	}

	dir := filepath.Dir(path)
	src, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("tags: open %q: %w", path, err)
	}

	tmp, err := os.CreateTemp(dir, tempPattern(path))
	if err != nil {
		_ = src.Close()
		return fmt.Errorf("tags: create temp for %q: %w", path, err)
	}
	_, copyErr := io.Copy(tmp, src)
	closeErr := tmp.Close()
	readErr := src.Close()
	if copyErr != nil || closeErr != nil || readErr != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("tags: copy %q: %w", path, err)
	}
	if err := os.Chmod(tmp.Name(), info.Mode().Perm()); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("tags: chmod temp for %q: %w", path, err)
	}

	// Every write, including the second MP3 TXXX pass, happens on the
	// copy; a failure here removes it and the original survives.
	if err := write(tmp.Name()); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}

	if err := syncFile(tmp.Name()); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("tags: sync temp for %q: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("tags: replace %q: %w", path, err)
	}
	// Best effort: persist the directory entry itself.
	syncDir(dir)
	return nil
}

// tempPrefix is the hidden temp name prefix used by tempPattern. Crashed
// tag writes can leave these copies behind; the library scan skips them
// and removes stale ones.
const tempPrefix = ".coda-tag-"

// IsTempFile reports whether name (base or full path) is a Coda tag-write
// temp file. It matches the tempPattern prefix, not the random suffix, so
// it also catches leftovers from crashed writes.
func IsTempFile(name string) bool {
	return strings.HasPrefix(filepath.Base(name), tempPrefix)
}

// tempPattern builds a hidden temp name in the same directory that keeps
// the original extension: taglib resolves file type by extension, so a
// bare ".tmp" name would fail to parse.
func tempPattern(path string) string {
	ext := filepath.Ext(path)
	if ext != "" {
		ext = "*" + ext
	}
	return tempPrefix + ext
}

// syncFile flushes a written file to stable storage before the rename.
func syncFile(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("sync: %w", err)
	}
	return f.Close()
}

// syncDir flushes a directory entry after a rename. Not every platform
// supports directory fsync; failures are deliberately ignored because the
// rename itself already succeeded.
func syncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
}
