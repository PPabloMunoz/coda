package library

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
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
