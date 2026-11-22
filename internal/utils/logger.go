package utils

import (
	"fmt"
	"io"
	"os"
	"sync/atomic"
)

type Logger struct {
	w     io.Writer
	debug atomic.Bool
}

// New returns a Logger writing to stderr with debug honors CODA_DEBUG/CODA_LOG_LEVEL.
func New() *Logger { return NewWithWriter(os.Stderr) }

// NewWithWriter returns a Logger writing to w with debug honors CODA_DEBUG/CODA_LOG_LEVEL.
func NewWithWriter(w io.Writer) *Logger {
	l := &Logger{w: w}
	l.debug.Store(debugFromEnv())
	return l
}

// SetDebug toggles debug output (used by tests and CLI flags).
func (l *Logger) SetDebug(enabled bool) { l.debug.Store(enabled) }

// DebugEnabled reports whether debug output is enabled.
func (l *Logger) DebugEnabled() bool { return l.debug.Load() }

// Info writes an INFO line.
func (l *Logger) Info(msg string) { l.print("INFO", msg) }

// Error writes an ERROR line.
func (l *Logger) Error(msg string) { l.print("ERROR", msg) }

// Infof writes a formatted INFO line.
func (l *Logger) Infof(format string, args ...any) { l.print("INFO", fmt.Sprintf(format, args...)) }

// Errorf writes a formatted ERROR line.
func (l *Logger) Errorf(format string, args ...any) { l.print("ERROR", fmt.Sprintf(format, args...)) }

// Warn writes a WARN line.
func (l *Logger) Warn(msg string) { l.print("WARN", msg) }

// Warnf writes a formatted WARN line.
func (l *Logger) Warnf(format string, args ...any) { l.print("WARN", fmt.Sprintf(format, args...)) }

// Debug writes a DEBUG line when debug is enabled.
func (l *Logger) Debug(msg string) {
	if l.debug.Load() {
		l.print("DEBUG", msg)
	}
}

// Debugf writes a formatted DEBUG line when debug is enabled.
func (l *Logger) Debugf(format string, args ...any) {
	if l.debug.Load() {
		l.print("DEBUG", fmt.Sprintf(format, args...))
	}
}
