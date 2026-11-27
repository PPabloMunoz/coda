//go:build !unix

package tags

import (
	"os"
)

// copyOwnership is a no-op where chown is unavailable (Windows). The
// replacement file keeps the process owner.
func copyOwnership(string, os.FileInfo) error {
	return nil
}
