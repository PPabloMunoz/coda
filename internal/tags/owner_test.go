package tags

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestMutateContinuesWhenOwnershipCopyFails stubs the platform ownership
// helper to force a chown error and asserts the tag write still commits.
// A failed chown (foreign owner, filesystem without chown) must never lose
// the write; it only warns.
func TestMutateContinuesWhenOwnershipCopyFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "song.wav")
	writeWav(t, path)

	prev := preserveOwnership
	preserveOwnership = func(string, os.FileInfo) error {
		return errors.New("chown denied")
	}
	defer func() { preserveOwnership = prev }()

	if err := mutate(path, func(tmp string) error {
		return os.WriteFile(tmp, []byte("rewritten"), 0o644)
	}); err != nil {
		t.Fatalf("mutate with failed chown: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "rewritten" {
		t.Errorf("content = %q, want rewritten (failed chown must not lose the write)", got)
	}
	if left := tempFilesLeft(t, dir); len(left) > 0 {
		t.Errorf("temp files left behind: %v", left)
	}
}
