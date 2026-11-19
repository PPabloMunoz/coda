package utils

import (
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"sync/atomic"
	"time"
)

type Logger struct {
	w     io.Writer
	debug atomic.Bool
}

func New() *Logger { return NewWithWriter(os.Stderr) }

func NewWithWriter(w io.Writer) *Logger {
	l := &Logger{w: w}
	l.debug.Store(debugFromEnv())
	return l
}

// SetDebug toggles debug output (used by tests and CLI flags).
func (l *Logger) SetDebug(enabled bool) { l.debug.Store(enabled) }

func (l *Logger) DebugEnabled() bool { return l.debug.Load() }

func (l *Logger) Info(msg string)                  { l.print("INFO", msg) }
func (l *Logger) Error(msg string)                 { l.print("ERROR", msg) }
func (l *Logger) Infof(format string, args ...any) { l.print("INFO", fmt.Sprintf(format, args...)) }

func (l *Logger) Errorf(format string, args ...any) { l.print("ERROR", fmt.Sprintf(format, args...)) }

func (l *Logger) Warn(msg string)                  { l.print("WARN", msg) }
func (l *Logger) Warnf(format string, args ...any) { l.print("WARN", fmt.Sprintf(format, args...)) }

func (l *Logger) Debug(msg string) {
	if l.debug.Load() {
		l.print("DEBUG", msg)
	}
}

func (l *Logger) Debugf(format string, args ...any) {
	if l.debug.Load() {
		l.print("DEBUG", fmt.Sprintf(format, args...))
	}
}

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
