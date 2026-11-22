package tags

import (
	"fmt"

	"go.senan.xyz/taglib"
)

// WriteCover embeds img as the file's front cover (index 0). Standard
// picture frame only, so it stays Navidrome-compatible like LinkKeys.
func WriteCover(path string, img []byte) error {
	if err := taglib.WriteImage(path, img); err != nil {
		return fmt.Errorf("tags: write cover %q: %w", path, err)
	}
	return nil
}

// ClearCover removes the embedded front cover (index 0).
func ClearCover(path string) error {
	if err := taglib.WriteImage(path, nil); err != nil {
		return fmt.Errorf("tags: clear cover %q: %w", path, err)
	}
	return nil
}
