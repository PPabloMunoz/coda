//go:build unix

package tags

import (
	"fmt"
	"os"
	"syscall"
)

// copyOwnership copies the original uid/gid onto the temp file so the
// atomic rename over path keeps the library owner's identity. Only the
// temp is touched; the original is never modified in place.
func copyOwnership(tmp string, info os.FileInfo) error {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st == nil {
		return fmt.Errorf("no unix ownership info")
	}
	if err := os.Chown(tmp, int(st.Uid), int(st.Gid)); err != nil {
		return fmt.Errorf("chown temp: %w", err)
	}
	return nil
}
