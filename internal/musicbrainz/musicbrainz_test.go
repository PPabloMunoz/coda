package musicbrainz

import (
	"context"
	"testing"
	"time"
)

func TestNewRequiresUserAgent(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Error("New(empty) = nil, want UserAgent error")
	}
	if _, err := New(Config{UserAgent: "   "}); err == nil {
		t.Error("New(blank UA) = nil, want error")
	}
}

func TestNewDefaults(t *testing.T) {
	c, err := New(Config{UserAgent: "coda-test/0.1 (test@example.com)"})
	if err != nil {
		t.Fatalf("New = %v, want nil", err)
	}
	if c.baseURL != DefaultBaseURL {
		t.Errorf("baseURL = %q, want %q", c.baseURL, DefaultBaseURL)
	}
	if c.maxRetries != 3 {
		t.Errorf("maxRetries = %d, want 3", c.maxRetries)
	}
	if c.http.Timeout != 15*time.Second {
		t.Errorf("timeout = %s, want 15s", c.http.Timeout)
	}

	c, err = New(Config{UserAgent: "ua", BaseURL: "http://example.com/", MaxRetries: 5, Timeout: time.Second})
	if err != nil {
		t.Fatalf("New = %v", err)
	}
	if c.baseURL != "http://example.com" {
		t.Errorf("trailing slash not trimmed: %q", c.baseURL)
	}
	if c.maxRetries != 5 {
		t.Errorf("maxRetries = %d, want 5", c.maxRetries)
	}
}

func TestBackoffDelay(t *testing.T) {
	if got := backoffDelay(0, ""); got != time.Second {
		t.Errorf("attempt 0 = %s, want 1s", got)
	}
	if got := backoffDelay(1, ""); got != 2*time.Second {
		t.Errorf("attempt 1 = %s, want 2s", got)
	}
	if got := backoffDelay(2, ""); got != 4*time.Second {
		t.Errorf("attempt 2 = %s, want 4s", got)
	}
	if got := backoffDelay(10, ""); got != 30*time.Second {
		t.Errorf("large attempt = %s, want capped 30s", got)
	}
	if got := backoffDelay(0, "5"); got != 5*time.Second {
		t.Errorf("Retry-After 5 = %s, want 5s", got)
	}
	if got := backoffDelay(0, "99"); got != 30*time.Second {
		t.Errorf("Retry-After 99 = %s, want capped 30s", got)
	}
	future := time.Now().Add(5 * time.Second).UTC().Format("Mon, 02 Jan 2006 15:04:05 GMT")
	if got := backoffDelay(0, future); got <= 0 || got > 6*time.Second {
		t.Errorf("Retry-After http-date = %s, want ~5s", got)
	}
}

func TestSleepOrDone(t *testing.T) {
	if !sleepOrDone(context.Background(), 0) {
		t.Error("sleepOrDone(bg, 0) = false, want true")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if sleepOrDone(ctx, 0) {
		t.Error("sleepOrDone(cancelled, 0) = true, want false")
	}
	if sleepOrDone(ctx, time.Second) {
		t.Error("sleepOrDone(cancelled, 1s) = true, want false")
	}
	if !sleepOrDone(context.Background(), time.Millisecond) {
		t.Error("sleepOrDone(bg, 1ms) = false, want true")
	}
}
