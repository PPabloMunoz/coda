package lock

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAcquireRelease(t *testing.T) {
	db := filepath.Join(t.TempDir(), "coda.db")
	l, err := Acquire(db)
	if err != nil {
		t.Fatalf("Acquire = %v", err)
	}
	want := db + ".lock"
	if l.Path() != want {
		t.Errorf("Path = %q, want %q", l.Path(), want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("lock file missing: %v", err)
	}
	raw, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("read lock file = %v", err)
	}
	if strings.TrimSpace(string(raw)) == "" {
		t.Error("lock file has no PID")
	}
	if err := l.Release(); err != nil {
		t.Fatalf("Release = %v", err)
	}
	// Re-acquire after release succeeds.
	l2, err := Acquire(db)
	if err != nil {
		t.Fatalf("re-Acquire = %v", err)
	}
	_ = l2.Release()
}

func TestSecondAcquireFails(t *testing.T) {
	db := filepath.Join(t.TempDir(), "coda.db")
	l, err := Acquire(db)
	if err != nil {
		t.Fatalf("Acquire = %v", err)
	}
	defer func() { _ = l.Release() }()
	if _, err := Acquire(db); err == nil {
		t.Fatal("second Acquire succeeded, want already-running error")
	}
}

func TestPathForDB(t *testing.T) {
	if got := PathForDB("/data/coda.db?_pragma=journal_mode(WAL)"); got != "/data/coda.db.lock" {
		t.Errorf("PathForDB DSN = %q", got)
	}
	if got := PathForDB(":memory:"); got != "" {
		t.Errorf("PathForDB :memory: = %q, want empty", got)
	}
}
