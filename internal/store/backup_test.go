package store

import (
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/pressly/goose/v3"

	"github.com/ppablomunoz/coda/internal/utils"
)

func gooseDownTo(t *testing.T, db *sql.DB, version int64) {
	t.Helper()
	goose.SetBaseFS(migrationsFS)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatalf("SetDialect = %v", err)
	}
	if err := goose.DownTo(db, "migrations", version); err != nil {
		t.Fatalf("DownTo(%d) = %v", version, err)
	}
}

// TestNoBackupOnFreshDB verifies a brand-new database gets no .bak file.
func TestNoBackupOnFreshDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	log := utils.NewWithWriter(io.Discard)
	st, err := NewStore(dbPath, nil, log)
	if err != nil {
		t.Fatalf("NewStore = %v", err)
	}
	st.Close()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir = %v", err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".bak" || len(e.Name()) > 4 && containsBak(e.Name()) {
			t.Errorf("unexpected backup file %q on fresh db", e.Name())
		}
	}
}

func containsBak(name string) bool {
	for i := 0; i+5 <= len(name); i++ {
		if name[i:i+5] == ".bak-" {
			return true
		}
	}
	return false
}

// TestBackupBeforeMigration rolls a database back to schema v1, then
// reopens it: startup must snapshot v1 to test.db.bak-0001 before
// migrating forward again.
func TestBackupBeforeMigration(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	log := utils.NewWithWriter(io.Discard)

	st, err := NewStore(dbPath, nil, log)
	if err != nil {
		t.Fatalf("NewStore = %v", err)
	}
	st.Close()

	// Roll back to v1 for a genuine old-schema database.
	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open raw = %v", err)
	}
	gooseDownTo(t, raw, 1)
	_ = raw.Close()

	st2, err := NewStore(dbPath, nil, log)
	if err != nil {
		t.Fatalf("NewStore after downgrade = %v", err)
	}
	st2.Close()

	backup := dbPath + ".bak-0001"
	info, err := os.Stat(backup)
	if err != nil {
		t.Fatalf("backup %q missing: %v", backup, err)
	}
	if info.Size() == 0 {
		t.Error("backup is empty")
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("backup perm = %o, want 600", perm)
	}
	// The backup must be a valid SQLite file still at v1.
	rawBackup, err := sql.Open("sqlite", backup)
	if err != nil {
		t.Fatalf("open backup = %v", err)
	}
	defer func() { _ = rawBackup.Close() }()
	var version int64
	if err := rawBackup.QueryRow(
		`SELECT version_id FROM goose_db_version WHERE is_applied = 1 ORDER BY id DESC LIMIT 1`,
	).Scan(&version); err != nil {
		t.Fatalf("backup version = %v", err)
	}
	if version != 1 {
		t.Errorf("backup version = %d, want 1", version)
	}

	// No leftover temp, and a second open does not overwrite the backup.
	if _, err := os.Stat(backup + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("backup tmp leftover present")
	}
	before, err := os.Stat(backup)
	if err != nil {
		t.Fatalf("stat backup = %v", err)
	}
	st3, err := NewStore(dbPath, nil, log)
	if err != nil {
		t.Fatalf("second NewStore = %v", err)
	}
	st3.Close()
	after, err := os.Stat(backup)
	if err != nil {
		t.Fatalf("stat backup = %v", err)
	}
	if !before.ModTime().Equal(after.ModTime()) {
		t.Error("backup was overwritten on second open")
	}
}
