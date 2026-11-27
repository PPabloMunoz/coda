package utils

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

func debugFromEnv() bool {
	if isTruthy(os.Getenv("CODA_DEBUG")) {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(os.Getenv("CODA_LOG_LEVEL")), "debug")
}

func isTruthy(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func (l *Logger) print(level, msg string) {
	_, _ = fmt.Fprintf(l.w, "%s %s %s\n", time.Now().Format("2006-01-02 15:04:05"), level, msg)
}

// Regex for valid MusicBrainz ID (UUID)
var (
	mbIDRegex      = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	ErrInvalidMBID = errors.New("invalid id")
)

// ValidateMBID reports whether id is a lowercase-hex MusicBrainz UUID.
func ValidateMBID(id string) error {
	if match := mbIDRegex.MatchString(id); !match {
		return ErrInvalidMBID
	}
	return nil
}

// NullIfEmpty returns nil for blank strings so DB columns stay NULL until matched.
func NullIfEmpty(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
}

// SanitizeSegment makes s safe for use as a single path segment, replacing
// separators, control characters, and reserved symbols and trimming edge dots.
// The result is capped at maxSegmentBytes bytes (UTF-8 safe) so multi-byte
// titles cannot exceed NAME_MAX, and Windows reserved device names
// (CON, PRN, AUX, NUL, COM1-9, LPT1-9, with or without an extension) are
// mangled so the segment is creatable on Windows and on case-insensitive
// filesystems.
func SanitizeSegment(s string) string {
	return sanitizeSegment(s, 100)
}

func sanitizeSegment(s string, maxBytes int) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	// Never allow separators to create sub-dirs / escapes.
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.ReplaceAll(s, "\\", "_")

	var b strings.Builder
	for _, r := range s {
		switch {
		case r < 32 || r == 127: // control / DEL
			b.WriteRune('_')
		case strings.ContainsRune(`:*?"<>|`, r):
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	s = strings.ReplaceAll(strings.TrimSpace(b.String()), " ", "_")
	s = strings.Trim(s, "._")
	// Collapse repeats + cap for ENAMETOOLONG (bytes, not runes: 100 CJK
	// characters would otherwise exceed the 255-byte NAME_MAX).
	for strings.Contains(s, "__") {
		s = strings.ReplaceAll(s, "__", "_")
	}
	s = truncateBytes(s, maxBytes)
	s = strings.Trim(s, "._")
	s = mangleReservedName(s, maxBytes)
	return s
}

// truncateBytes cuts s to at most max bytes on a UTF-8 boundary.
func truncateBytes(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.ValidString(s[:cut]) {
		cut--
	}
	return s[:cut]
}

// reservedBases are Windows device names that cannot be used as a file
// name, with or without an extension, on Windows and on some
// case-insensitive filesystems.
var reservedBases = map[string]struct{}{
	"CON": {}, "PRN": {}, "AUX": {}, "NUL": {},
	"COM1": {}, "COM2": {}, "COM3": {}, "COM4": {}, "COM5": {},
	"COM6": {}, "COM7": {}, "COM8": {}, "COM9": {},
	"LPT1": {}, "LPT2": {}, "LPT3": {}, "LPT4": {}, "LPT5": {},
	"LPT6": {}, "LPT7": {}, "LPT8": {}, "LPT9": {},
}

// mangleReservedName appends "_" to the base of Windows reserved device
// names ("NUL" -> "NUL_", "con.txt" -> "con_.txt") so the segment can be
// created everywhere. The result stays within max bytes.
func mangleReservedName(s string, max int) string {
	if s == "" {
		return s
	}
	base := s
	rest := ""
	if i := strings.IndexByte(s, '.'); i >= 0 {
		base, rest = s[:i], s[i:]
	}
	if _, ok := reservedBases[strings.ToUpper(base)]; !ok {
		return s
	}
	mangled := base + "_" + rest
	if len(mangled) <= max {
		return mangled
	}
	// No room for the extra byte: drop a byte from the base, then mangle.
	trimmed := truncateBytes(base, max-len(rest)-1)
	trimmed = strings.Trim(trimmed, "._")
	if trimmed == "" {
		trimmed = "_"
	}
	return trimmed + "_" + rest
}

// GetFileFormat extracts the file extension from a path without the leading dot,
// normalized to lowercase. Returns an empty string if there is no extension.
func GetFileFormat(path string) string {
	ext := filepath.Ext(path)
	return strings.ToLower(strings.TrimPrefix(ext, "."))
}
