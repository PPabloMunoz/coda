//go:build unix

package tasks

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func statOwner(t *testing.T, path string) (uint32, uint32) {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || st == nil {
		t.Skip("no unix ownership info for test file")
	}
	return st.Uid, st.Gid
}

// TestCopyFileNoReplacePreservesOwnership chowns the source to the current
// uid plus a supplementary gid (a chown the file owner is permitted to
// make), runs the copy-fallback move, and asserts the destination kept
// both. Skips as root, where any chown succeeds and the non-root case
// cannot be exercised, or where no alternate group is available.
func TestCopyFileNoReplacePreservesOwnership(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: ownership preservation is trivially permitted")
	}
	uid, egid := os.Getuid(), os.Getegid()
	groups, err := os.Getgroups()
	if err != nil {
		t.Skipf("cannot list groups: %v", err)
	}
	targetGid := -1
	for _, g := range groups {
		if g != egid {
			targetGid = g
			break
		}
	}
	if targetGid < 0 {
		t.Skip("no supplementary group different from the process gid")
	}

	dir := t.TempDir()
	src := filepath.Join(dir, "src.wav")
	dst := filepath.Join(dir, "sub", "dst.wav")
	if err := os.WriteFile(src, []byte("audio"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(src, uid, targetGid); err != nil {
		t.Skipf("chown to uid:gid %d:%d not permitted: %v", uid, targetGid, err)
	}

	if err := copyFileNoReplace("test", 1, "track", src, dst); err != nil {
		t.Fatalf("copy = %v", err)
	}

	gotUID, gotGID := statOwner(t, dst)
	if gotUID != uint32(uid) || gotGID != uint32(targetGid) {
		t.Errorf("owner = %d:%d, want %d:%d (copy lost ownership)",
			gotUID, gotGID, uid, targetGid)
	}
	if fi, _ := os.Stat(dst); fi.Mode().Perm() != 0o640 {
		t.Errorf("dst mode = %v, want 0640 preserved", fi.Mode())
	}
}
