package config

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/ppablomunoz/coda/internal/utils"
)

// CoverDirName is the directory (sibling of the DB file) holding cached
// release cover bytes. One file per release, derived from the release MBID.
const CoverDirName = "covers"

// CoverDirFromDBPath derives the cover cache dir from a DB DSN, which may
// carry SQLite pragma query params (see Pragmas / CODA_DB_PATH). The DSN is
// passed through untouched elsewhere; only the local file part is used here.
func CoverDirFromDBPath(dsn string) string {
	path, _, _ := strings.Cut(dsn, "?")
	return filepath.Join(filepath.Dir(path), CoverDirName)
}

// ExtFromMIME maps a stored cover MIME type to its file extension.
func ExtFromMIME(mime string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(mime)) {
	case "image/jpeg":
		return "jpg", nil
	case "image/png":
		return "png", nil
	case "image/webp":
		return "webp", nil
	default:
		return "", fmt.Errorf("config: unsupported cover mime %q", mime)
	}
}

// CoverPath returns the deterministic cache path for a release cover.
// Paths are never stored in the DB (keeps rows portable when the data dir
// moves); callers recompute them from the cover dir + release MBID + mime.
// Manual uploads reuse the same layout with source='upload'.
func CoverPath(coverDir, releaseID, mime string) (string, error) {
	if err := utils.ValidateMBID(releaseID); err != nil {
		return "", fmt.Errorf("config: invalid release id: %w", err)
	}
	ext, err := ExtFromMIME(mime)
	if err != nil {
		return "", err
	}
	return filepath.Join(coverDir, releaseID+"."+ext), nil
}

// ArtistCoverPath returns the deterministic cache path for an artist photo.
// Artist files carry an "artist-" prefix so they never collide with release
// files. Display-only: artist art is never embedded into audio files.
func ArtistCoverPath(coverDir, artistID, mime string) (string, error) {
	if err := utils.ValidateMBID(artistID); err != nil {
		return "", fmt.Errorf("config: invalid artist id: %w", err)
	}
	ext, err := ExtFromMIME(mime)
	if err != nil {
		return "", err
	}
	return filepath.Join(coverDir, "artist-"+artistID+"."+ext), nil
}
