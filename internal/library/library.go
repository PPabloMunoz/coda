package library

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/ppablomunoz/coda/internal/utils"
)

// SupportedFormats available to scan.
// Using map[string]struct{} for O(1) time
var SupportedFormats = map[string]struct{}{
	"aac": {}, "aif": {}, "aiff": {},
	"alac": {}, "ape": {}, "flac": {},
	"m4a": {}, "m4b": {}, "mp3": {},
	"mpc": {}, "oga": {}, "ogg": {},
	"opus": {}, "wav": {}, "wma": {},
	"wv": {},
}

type File struct {
	LibraryRoot string
	Path        string
	SizeBytes   int64
	MtimeNs     int64  // UnixNano, maps to local_files.mtime_ns NOT NULL
	FormatHint  string // "mp3", no ".mp3"
}

// RootStatus reports how completely a single root was walked. Walked is
// false when the root itself was missing/unreadable or any entry under it
// could not be read; WalkErr holds the first walk error encountered.
// Files counts the audio files yielded for the root.
type RootStatus struct {
	Root    string
	Files   int
	Walked  bool
	WalkErr error
}

// Scan walks rootPaths and calls fn for each supported audio file, skipping
// symlinks, non-regular files, unreadable entries, and hidden files and
// directories (any base name starting with "."). This keeps tag-write temp
// copies (".coda-tag-*"), AppleDouble "._*" files, trash, and similar
// dotfiles out of the library. A root whose own name starts with "." is
// still scanned; only entries below it are skipped. It never follows symlinks.
//
// A per-root status is returned alongside the error: a missing/unreadable
// root or subdirectory marks that root Walked=false (recording the first
// walk error) and continues with the remaining roots instead of failing the
// scan. Callback errors and context cancellation still abort the whole scan.
func Scan(ctx context.Context, rootPaths []string, fn func(File) error) ([]RootStatus, error) {
	if len(rootPaths) == 0 {
		return nil, nil
	}

	statuses := make([]RootStatus, 0, len(rootPaths))
	for _, r := range rootPaths {
		status := RootStatus{Root: r, Walked: true}
		err := filepath.WalkDir(r, func(path string, d fs.DirEntry, err error) error {
			select {
			case <-ctx.Done():
				return ctx.Err() // aborts WalkDir
			default:
			}

			if err != nil { // skip unreadable file/dir, mark root incomplete
				status.Walked = false
				if status.WalkErr == nil {
					status.WalkErr = err
				}
				return nil
			}

			if d.IsDir() {
				if path != r && strings.HasPrefix(d.Name(), ".") {
					return filepath.SkipDir
				}
				return nil
			}

			if strings.HasPrefix(d.Name(), ".") {
				return nil // hidden file (covers .coda-tag-* temps)
			}

			if d.Type()&fs.ModeSymlink != 0 {
				return nil // ignore file + dir links, never follow
			}

			info, err := d.Info()
			if err != nil {
				return nil // disappeared mid-walk, skip
			}

			if !info.Mode().IsRegular() {
				return nil // socket, fifo, device, symlink-to-dir
			}

			ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))
			if _, ok := SupportedFormats[ext]; !ok {
				return nil
			}

			if err := fn(File{
				LibraryRoot: r,
				Path:        path,
				SizeBytes:   info.Size(),
				MtimeNs:     info.ModTime().UnixNano(),
				FormatHint:  ext,
			}); err != nil {
				return err
			}
			status.Files++
			return nil
		})
		if err != nil {
			statuses = append(statuses, status)
			return statuses, fmt.Errorf("library: walk root %q: %w", r, err)
		}
		statuses = append(statuses, status)
	}

	return statuses, nil
}

type Target struct {
	Root       string
	Artist     string
	Album      string
	Title      string
	Disc       int
	Track      int
	TotalDiscs int    // <=1 omits the disc prefix; >1 keeps DD-TT_
	Ext        string // without dot
}

// Path returns the sanitized Artist/Album destination under Root. Single-disc
// releases (TotalDiscs <= 1) use TT_Title.ext; multi-disc releases keep
// DD-TT_Title.ext. It rejects empty fields, unsupported extensions, and
// escapes from Root.
func (t Target) Path() (string, error) {
	root := filepath.Clean(strings.TrimSpace(t.Root))
	if root == "" || root == "." {
		return "", fmt.Errorf("library: destination root is empty")
	}
	if !filepath.IsAbs(root) {
		return "", fmt.Errorf("library: destination root %q is not absolute", t.Root)
	}

	artist := utils.SanitizeSegment(t.Artist)
	if artist == "" {
		return "", fmt.Errorf("library: artist is empty")
	}
	album := utils.SanitizeSegment(t.Album)
	if album == "" {
		return "", fmt.Errorf("library: album is empty")
	}
	title := utils.SanitizeSegment(t.Title)
	if title == "" {
		return "", fmt.Errorf("library: title is empty")
	}
	if t.Disc < 1 {
		return "", fmt.Errorf("library: disc %d out of range", t.Disc)
	}
	if t.Track < 1 {
		return "", fmt.Errorf("library: track %d out of range", t.Track)
	}

	ext := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(t.Ext), "."))
	if ext == "" {
		return "", fmt.Errorf("library: extension is empty")
	}
	if _, ok := SupportedFormats[ext]; !ok {
		return "", fmt.Errorf("library: unsupported extension %q", t.Ext)
	}

	dst := filepath.Join(root, artist, album,
		targetFilename(t.Disc, t.Track, t.TotalDiscs, title, ext))

	rel, err := filepath.Rel(root, dst)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("library: destination escapes root")
	}
	return dst, nil
}

// targetFilename omits the disc prefix for single-disc releases (TotalDiscs <=
// 1, the zero-value default) so files are named TT_Title.ext; multi-disc
// releases keep DD-TT_Title.ext to avoid collisions across discs.
func targetFilename(disc, track, totalDiscs int, title, ext string) string {
	if totalDiscs > 1 {
		return fmt.Sprintf("%02d-%02d_%s.%s", disc, track, title, ext)
	}
	return fmt.Sprintf("%02d_%s.%s", track, title, ext)
}
