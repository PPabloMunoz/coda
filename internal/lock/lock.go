package lock

import (
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Lock is an exclusive host-local guard for one Coda data directory. It
// holds an open file descriptor on the lock file for its lifetime; the OS
// releases the advisory lock when the process exits or crashes, so the
// file itself is never unlinked (unlinking would let a second process lock
// a new inode while the first still runs).
type Lock struct {
	f    *os.File
	path string
}

// PathForDB returns the sidecar lock path for a database file. The DSN may
// carry SQLite pragma query params (see config.Pragmas); only the local
// file part is used.
func PathForDB(dbPath string) string {
	path, _, _ := strings.Cut(dbPath, "?")
	if path == "" || path == ":memory:" {
		return ""
	}
	return path + ".lock"
}

// Path returns the lock file path.
func (l *Lock) Path() string {
	return l.path
}

// Release drops the lock. The file stays on disk for the next startup.
func (l *Lock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	return l.f.Close()
}

// openLockFile creates the lock dir and opens (never truncates before the
// lock is held, so a concurrent holder's PID is not clobbered).
func openLockFile(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	return os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
}

// writePID records the owner's PID for diagnostics. Best-effort: the lock
// itself is the guard, the PID is only a hint.
func writePID(f *os.File) {
	pid := strconv.Itoa(os.Getpid())
	_ = f.Truncate(0)
	_, _ = f.Seek(0, io.SeekStart)
	_, _ = f.WriteString(pid + "\n")
	_ = f.Sync()
}
