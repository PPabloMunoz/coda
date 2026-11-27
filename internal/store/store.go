package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"

	apperrors "github.com/ppablomunoz/coda/internal/errors"
	"github.com/ppablomunoz/coda/internal/utils"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

type Store struct {
	db        *sql.DB
	rootPaths []string
	log       *utils.Logger
}

// NewStore opens the SQLite DB (single connection), runs goose migrations, and
// returns a Store with the configured roots for later AddRootDirs/SyncLocalFiles.
func NewStore(dsn string, rootPaths []string, log *utils.Logger) (*Store, error) {
	db, err := openDB(dsn, log)
	if err != nil {
		return nil, err
	}
	return &Store{db: db, rootPaths: rootPaths, log: log}, nil
}

// AddRootDirs upserts configured roots as active and soft-deactivates roots
// removed from config (rows and links are kept for a future re-add).
func (st *Store) AddRootDirs(ctx context.Context) error {
	tx, err := st.db.BeginTx(ctx, nil)
	if err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("start tx: %w", err),
		)
	}
	defer func() { _ = tx.Rollback() }()

	for _, r := range st.rootPaths {
		_, execErr := tx.ExecContext(ctx, `
			INSERT INTO roots(path, active, created_at)
			VALUES (?, 1, strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
			ON CONFLICT(path) DO UPDATE SET active=1`,
			r,
		)
		if execErr != nil {
			return apperrors.Wrap(
				apperrors.ErrInternal,
				fmt.Errorf("error inserting root %q into DB: %w", r, execErr),
			)
		}
	}

	if len(st.rootPaths) == 0 {
		_, err := tx.ExecContext(ctx, `UPDATE roots SET active=0 WHERE active=1`)
		if err != nil {
			return apperrors.Wrap(
				apperrors.ErrInternal,
				fmt.Errorf("deactivate roots: %w", err),
			)
		}
	} else {
		ph := placeholders(len(st.rootPaths))
		args := make([]any, len(st.rootPaths))
		for i, r := range st.rootPaths {
			args[i] = r
		}
		_, err := tx.ExecContext(ctx,
			`UPDATE roots SET active=0 WHERE path NOT IN (`+ph+`)`,
			args...,
		)
		if err != nil {
			return apperrors.Wrap(
				apperrors.ErrInternal,
				fmt.Errorf("deactivate roots: %w", err),
			)
		}
	}

	if err := tx.Commit(); err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("add root dirs: %w", err),
		)
	}

	return nil
}

// Close closes the underlying DB connection.
func (st *Store) Close() {
	_ = st.db.Close()
}

// RootPaths returns a copy of the configured library roots.
func (st *Store) RootPaths() []string {
	return append([]string(nil), st.rootPaths...)
}

// openDB creates the DB dir, opens SQLite with MaxOpenConns(1), and runs migrations.
func openDB(dsn string, log *utils.Logger) (*sql.DB, error) {
	path, _, _ := strings.Cut(dsn, "?")
	dir := filepath.Dir(path)
	if dir == "" {
		dir = "."
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("store: create dirs for db: %w", err)
	}

	// A database file that does not exist yet is fresh: migrations build
	// it from scratch and there is nothing to back up. The check runs
	// before sql.Open because opening creates the file.
	fresh := false
	if path != "" && path != ":memory:" {
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			fresh = true
		}
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open db: %w", err)
	}

	db.SetMaxOpenConns(1)

	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: connect to db: %w", err)
	}

	log.Debug("db migration start")
	if !fresh {
		if err := maybeBackupBeforeMigration(db, dsn, log); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("store: backup db: %w", err)
		}
	}
	if err := migration(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: migrate db: %w", err)
	}
	log.Debug("db migration done")

	return db, nil
}

// maybeBackupBeforeMigration snapshots the database file before goose
// applies pending migrations. Fresh databases (no file yet) and
// up-to-date databases need no backup. The snapshot is a consistent
// VACUUM INTO copy (safe with WAL active; a plain file copy can miss
// committed WAL frames) written to a temp file, chmodded 0600, and
// atomically renamed to <db>.bak-<schema-version>. One backup per schema
// version is kept and never overwritten. A backup failure aborts startup
// so a migration never runs unprotected.
func maybeBackupBeforeMigration(db *sql.DB, dsn string, log *utils.Logger) error {
	goose.SetBaseFS(migrationsFS)
	if err := goose.SetDialect("sqlite3"); err != nil {
		return err
	}

	path, _, _ := strings.Cut(dsn, "?")
	if path == "" || path == ":memory:" {
		return nil
	}
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}

	current, err := goose.GetDBVersion(db)
	if err != nil {
		return err
	}
	pending, err := goose.CollectMigrations("migrations", current, math.MaxInt64)
	if err != nil {
		if errors.Is(err, goose.ErrNoMigrationFiles) {
			return nil
		}
		return err
	}
	if len(pending) == 0 {
		return nil
	}

	backupPath := fmt.Sprintf("%s.bak-%04d", path, current)
	if _, err := os.Stat(backupPath); err == nil {
		log.Debugf("db backup %s exists, skipping", backupPath)
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	tmp := backupPath + ".tmp"
	_ = os.Remove(tmp)
	escaped := strings.ReplaceAll(tmp, "'", "''")
	if _, err := db.Exec(`VACUUM INTO '` + escaped + `'`); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("vacuum into backup: %w", err)
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, backupPath); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	log.Infof("db backup before migration: %s (schema v%d, %d pending)",
		backupPath, current, len(pending))
	return nil
}

// migration applies embedded goose migrations to db.
func migration(db *sql.DB) error {
	goose.SetBaseFS(migrationsFS)
	if err := goose.SetDialect("sqlite3"); err != nil {
		return err
	}
	if err := goose.Up(db, "migrations"); err != nil {
		return err
	}
	return nil
}

// placeholders returns "?,...,?" with n marks for IN clauses; empty for n <= 0.
func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat("?,", n-1) + "?"
}

// nullString converts s to *string, returning nil when NULL.
func nullString(s sql.NullString) *string {
	if s.Valid {
		return &s.String
	}
	return nil
}
