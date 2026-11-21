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
