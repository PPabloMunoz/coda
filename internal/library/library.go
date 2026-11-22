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

// Scan walks rootPaths and calls fn for each supported audio file, skipping
// symlinks, non-regular files, and unreadable entries. It never follows symlinks.
func Scan(ctx context.Context, rootPaths []string, fn func(File) error) error {
	if len(rootPaths) == 0 {
		return nil
	}

	for _, r := range rootPaths {
		err := filepath.WalkDir(r, func(path string, d fs.DirEntry, err error) error {
			select {
			case <-ctx.Done():
				return ctx.Err() // aborts WalkDir
			default:
			}

			if err != nil { // skip unreadable file/dir
				return nil
			}

			if d.IsDir() {
				return nil
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

			return fn(File{
				LibraryRoot: r,
				Path:        path,
				SizeBytes:   info.Size(),
				MtimeNs:     info.ModTime().UnixNano(),
				FormatHint:  ext,
			})
		})
		if err != nil {
			return fmt.Errorf("library: walk root %q: %w", r, err)
		}
	}

	return nil
}

type Target struct {
	Root   string
	Artist string
	Album  string
	Title  string
	Disc   int
	Track  int
	Ext    string // without dot
}

// Path returns the sanitized Artist/Album/02-03_Title.ext destination under
// Root, rejecting empty fields, unsupported extensions, and escapes from Root.
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
		fmt.Sprintf("%02d-%02d_%s.%s", t.Disc, t.Track, title, ext))

	rel, err := filepath.Rel(root, dst)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("library: destination escapes root")
	}
	return dst, nil
}
