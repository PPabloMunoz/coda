package store

import (
	"testing"
	"time"
)

// Timestamps written by the store must be RFC3339 UTC so they parse in all
// browsers (issue #48). The 0011 migration normalizes pre-existing
// space-format rows.
func TestTimestampsAreRFC3339(t *testing.T) {
	root := t.TempDir()
	st := newTestStore(t, root)

	got, err := st.GetRoot(ctx(), testRootID(t, st))
	if err != nil {
		t.Fatalf("GetRoot = %v", err)
	}
	if _, err := time.Parse(time.RFC3339, got.CreatedAt); err != nil {
		t.Errorf("roots.created_at = %q, want RFC3339: %v", got.CreatedAt, err)
	}
}

func TestMigration0011NormalizesLegacyTimestamps(t *testing.T) {
	root := t.TempDir()
	st := newTestStore(t, root)
	rootID := testRootID(t, st)

	// Simulate a pre-0011 row with the old CURRENT_TIMESTAMP format.
	if _, err := st.db.Exec(
		`INSERT INTO local_files (root_id, path, size_bytes, mtime_ns, format_hint, scanned_at, track_id)
		 VALUES (?, ?, 100, 123, 'mp3', '2024-01-01 00:00:00', NULL)`,
		rootID, root+"/legacy.mp3",
	); err != nil {
		t.Fatalf("insert legacy = %v", err)
	}
	if _, err := st.db.Exec(`UPDATE roots SET created_at = '2024-01-01 00:00:00' WHERE id = ?`, rootID); err != nil {
		t.Fatalf("backdate root = %v", err)
	}

	// Apply the same normalization the 0011 migration runs.
	for _, q := range []string{
		`UPDATE roots SET created_at = strftime('%Y-%m-%dT%H:%M:%SZ', created_at) WHERE created_at IS NOT NULL AND instr(created_at, 'T') = 0`,
		`UPDATE local_files SET scanned_at = strftime('%Y-%m-%dT%H:%M:%SZ', scanned_at) WHERE scanned_at IS NOT NULL AND instr(scanned_at, 'T') = 0`,
	} {
		if _, err := st.db.Exec(q); err != nil {
			t.Fatalf("normalize = %v", err)
		}
	}

	var created, scanned string
	if err := st.db.QueryRow(`SELECT created_at FROM roots WHERE id = ?`, rootID).Scan(&created); err != nil {
		t.Fatal(err)
	}
	if err := st.db.QueryRow(`SELECT scanned_at FROM local_files WHERE path = ?`, root+"/legacy.mp3").Scan(&scanned); err != nil {
		t.Fatal(err)
	}
	for name, v := range map[string]string{"created_at": created, "scanned_at": scanned} {
		parsed, err := time.Parse(time.RFC3339, v)
		if err != nil {
			t.Errorf("%s = %q, want RFC3339: %v", name, v, err)
			continue
		}
		if want := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC); !parsed.Equal(want) {
			t.Errorf("%s = %v, want %v (instant preserved)", name, parsed, want)
		}
	}
}
