package utils

import (
	"bytes"
	"strings"
	"testing"
)

func TestLoggerDebugGated(t *testing.T) {
	var buf bytes.Buffer
	l := NewWithWriter(&buf)
	l.SetDebug(false)
	l.Debug("hidden")
	l.Debugf("hidden %d", 1)
	if buf.Len() != 0 {
		t.Errorf("debug output when disabled = %q, want empty", buf.String())
	}

	l.SetDebug(true)
	if !l.DebugEnabled() {
		t.Error("DebugEnabled() = false, want true")
	}
	l.Debug("shown")
	if !strings.Contains(buf.String(), "shown") {
		t.Errorf("debug output = %q, want to contain %q", buf.String(), "shown")
	}
}

func TestLoggerLevels(t *testing.T) {
	var buf bytes.Buffer
	l := NewWithWriter(&buf)
	l.SetDebug(true)

	l.Info("info-msg")
	l.Infof("info-%d", 1)
	l.Error("error-msg")
	l.Errorf("error-%d", 2)
	l.Warn("warn-msg")
	l.Warnf("warn-%d", 3)
	l.Debug("debug-msg")

	out := buf.String()
	for _, want := range []string{"INFO info-msg", "INFO info-1", "ERROR error-msg", "ERROR error-2", "WARN warn-msg", "WARN warn-3", "DEBUG debug-msg"} {
		if !strings.Contains(out, want) {
			t.Errorf("log output missing %q in:\n%s", want, out)
		}
	}
}

func TestDebugFromEnv(t *testing.T) {
	t.Setenv("CODA_DEBUG", "1")
	t.Setenv("CODA_LOG_LEVEL", "")
	if !debugFromEnv() {
		t.Error("debugFromEnv with CODA_DEBUG=1 = false, want true")
	}

	t.Setenv("CODA_DEBUG", "")
	t.Setenv("CODA_LOG_LEVEL", "debug")
	if !debugFromEnv() {
		t.Error("debugFromEnv with CODA_LOG_LEVEL=debug = false, want true")
	}

	t.Setenv("CODA_DEBUG", "")
	t.Setenv("CODA_LOG_LEVEL", "")
	if debugFromEnv() {
		t.Error("debugFromEnv with no env = true, want false")
	}
}
