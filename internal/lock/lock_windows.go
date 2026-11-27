package lock

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// Acquire takes the exclusive non-blocking lock for the data directory
// holding dbPath (pragma query params stripped). A second Coda process on
// the same data dir fails fast with a clear error instead of scanning,
// moving, and tagging files concurrently. The lock is advisory and
// host-local: it does not guard network filesystems.
func Acquire(dbPath string) (*Lock, error) {
	path := PathForDB(dbPath)
	if path == "" {
		return nil, fmt.Errorf("lock: cannot lock in-memory database")
	}
	f, err := openLockFile(path)
	if err != nil {
		return nil, fmt.Errorf("lock: open %s: %w", path, err)
	}
	// Lock the first byte exclusively without blocking. An overlapped
	// struct is required; a zero value locks from the current offset.
	var ol windows.Overlapped
	err = windows.LockFileEx(
		windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, &ol,
	)
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf(
			"another coda instance is already running (lock file %s); stop it first: %w",
			path, err,
		)
	}
	writePID(f)
	return &Lock{f: f, path: path}, nil
}
