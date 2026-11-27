//go:build unix

package tasks

import (
	"fmt"
	"os"
	"syscall"
)

// copyOwnership copies the original uid/gid onto the move destination so
// the copy fallback in copyFileNoReplace keeps the library owner's
// identity, like the hard-link path does by keeping the same inode. Only
// the destination is touched; the source is never modified.
func copyOwnership(dst string, info os.FileInfo) error {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st == nil {
		return fmt.Errorf("no unix ownership info")
	}
	if err := os.Chown(dst, int(st.Uid), int(st.Gid)); err != nil {
		return fmt.Errorf("chown destination: %w", err)
	}
	return nil
}
