//go:build !unix

package tasks

import (
	"os"
)

// copyOwnership is a no-op where chown is unavailable (Windows). The
// move destination keeps the process owner.
func copyOwnership(string, os.FileInfo) error {
	return nil
}
