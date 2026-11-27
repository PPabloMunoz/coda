//go:build unix

package tags

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

// TestMutatePreservesOwnership chowns the source to the current uid plus a
// supplementary gid (a chown the file owner is permitted to make), runs
// mutate, and asserts the replacement kept both. Skips as root, where any
// chown succeeds and the non-root case cannot be exercised, or where no
// alternate group is available.
func TestMutatePreservesOwnership(t *testing.T) {
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
	path := filepath.Join(dir, "song.wav")
	writeWav(t, path)
	if err := os.Chown(path, uid, targetGid); err != nil {
		t.Skipf("chown to uid:gid %d:%d not permitted: %v", uid, targetGid, err)
	}

	if err := mutate(path, func(tmp string) error {
		return os.WriteFile(tmp, []byte("rewritten"), 0o644)
	}); err != nil {
		t.Fatalf("mutate: %v", err)
	}

	gotUID, gotGID := statOwner(t, path)
	if gotUID != uint32(uid) || gotGID != uint32(targetGid) {
		t.Errorf("owner = %d:%d, want %d:%d (replacement inode lost ownership)",
			gotUID, gotGID, uid, targetGid)
	}
	if left := tempFilesLeft(t, dir); len(left) > 0 {
		t.Errorf("temp files left behind: %v", left)
	}
}
