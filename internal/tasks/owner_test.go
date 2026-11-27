package tasks

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestCopyFileNoReplaceContinuesWhenOwnershipCopyFails stubs the platform
// ownership helper to force a chown error and asserts the move still
// commits. A failed chown (foreign owner, filesystem without chown) must
// never lose the file; it only warns.
func TestCopyFileNoReplaceContinuesWhenOwnershipCopyFails(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.wav")
	dst := filepath.Join(dir, "sub", "dst.wav")
	if err := os.WriteFile(src, []byte("audio"), 0o640); err != nil {
		t.Fatal(err)
	}

	prev := preserveOwnership
	preserveOwnership = func(string, os.FileInfo) error {
		return errors.New("chown denied")
	}
	defer func() { preserveOwnership = prev }()

	if err := copyFileNoReplace("test", 1, "track", src, dst); err != nil {
		t.Fatalf("copy with failed chown: %v", err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("src still present after copy-move: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil || string(got) != "audio" {
		t.Fatalf("dst = %q, %v; want audio (failed chown must not lose the move)", got, err)
	}
}
