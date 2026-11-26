package tags

import (
	"fmt"

	"go.senan.xyz/taglib"
)

// WriteCover embeds img as the file's front cover (index 0). Standard
// picture frame only, so it stays Navidrome-compatible like LinkKeys.
// The write lands on a temp copy that replaces path atomically.
func WriteCover(path string, img []byte) error {
	return mutate(path, func(tmp string) error {
		if err := taglib.WriteImage(tmp, img); err != nil {
			return fmt.Errorf("tags: write cover %q: %w", path, err)
		}
		return nil
	})
}

// ClearCover removes the embedded front cover (index 0). The write lands
// on a temp copy that replaces path atomically.
func ClearCover(path string) error {
	return mutate(path, func(tmp string) error {
		if err := taglib.WriteImage(tmp, nil); err != nil {
			return fmt.Errorf("tags: clear cover %q: %w", path, err)
		}
		return nil
	})
}
