package utils

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
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

func ValidateMBID(id string) error {
	if match := mbIDRegex.MatchString(id); !match {
		return ErrInvalidMBID
	}
	return nil
}

func NullIfEmpty(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
}

func SanitizeSegment(s string) string {
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
	// Collapse repeats + cap for ENAMETOOLONG.
	for strings.Contains(s, "__") {
		s = strings.ReplaceAll(s, "__", "_")
	}
	if len([]rune(s)) > 100 {
		s = string([]rune(s)[:100])
		s = strings.Trim(s, "._")
	}
	return s
}

// GetFileFormat extracts the file extension from a path without the leading dot,
// normalized to lowercase. Returns an empty string if there is no extension.
func GetFileFormat(path string) string {
	ext := filepath.Ext(path)
	return strings.ToLower(strings.TrimPrefix(ext, "."))
}
