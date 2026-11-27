package server

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ppablomunoz/coda/internal/artwork"
)

// TestSecurityHeaders checks the hardening headers on API and UI responses.
func TestSecurityHeaders(t *testing.T) {
	env := newTestEnv(t)

	for _, path := range []string{"/api/v1/health", "/"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Host = "localhost"
		w := httptest.NewRecorder()
		env.router.ServeHTTP(w, req)
		if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("%s X-Content-Type-Options = %q, want nosniff", path, got)
		}
		if got := w.Header().Get("Referrer-Policy"); got != "no-referrer" {
			t.Errorf("%s Referrer-Policy = %q, want no-referrer", path, got)
		}
		csp := w.Header().Get("Content-Security-Policy")
		for _, want := range []string{"default-src 'none'", "script-src 'self'", "style-src 'self'"} {
			if !strings.Contains(csp, want) {
				t.Errorf("%s CSP = %q, want substring %q", path, csp, want)
			}
		}
		if strings.Contains(csp, "'unsafe-inline'") {
			t.Errorf("%s CSP = %q, must not contain unsafe-inline", path, csp)
		}
	}
}

// TestHostAllowlist checks localhost/IP acceptance, unknown-host 421, and
// configured extra names.
func TestHostAllowlist(t *testing.T) {
	newEnvWithHosts := func(t *testing.T, hosts ...string) *testEnv {
		t.Helper()
		return newTestEnvWithArtAndOptions(t, nil, Options{AllowedHosts: hosts})
	}

	t.Run("localhost and IPs allowed", func(t *testing.T) {
		env := newEnvWithHosts(t)
		for _, host := range []string{
			"localhost", "localhost:8687",
			"127.0.0.1", "127.0.0.1:8687",
			"192.168.1.10:8687", "[::1]", "[::1]:8687",
		} {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
			req.Host = host
			w := httptest.NewRecorder()
			env.router.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Errorf("host %q = %d, want 200", host, w.Code)
			}
		}
	})

	t.Run("unknown hostname rejected", func(t *testing.T) {
		env := newEnvWithHosts(t)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
		req.Host = "evil.example.com"
		w := httptest.NewRecorder()
		env.router.ServeHTTP(w, req)
		if w.Code != http.StatusMisdirectedRequest {
			t.Fatalf("evil host = %d, want 421", w.Code)
		}
		if !strings.Contains(w.Body.String(), "INVALID_HOST") {
			t.Errorf("body = %q, want INVALID_HOST", w.Body.String())
		}
		// Security headers still apply to rejections.
		if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
		}
	})

	t.Run("configured hosts allowed", func(t *testing.T) {
		env := newEnvWithHosts(t, "Coda.LAN", "nas.local.")
		for _, host := range []string{"coda.lan", "coda.lan:8687", "nas.local", "NAS.LOCAL:8687"} {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
			req.Host = host
			w := httptest.NewRecorder()
			env.router.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Errorf("host %q = %d, want 200", host, w.Code)
			}
		}
		// Still unknown.
		req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
		req.Host = "other.lan"
		w := httptest.NewRecorder()
		env.router.ServeHTTP(w, req)
		if w.Code != http.StatusMisdirectedRequest {
			t.Errorf("other.lan = %d, want 421", w.Code)
		}
	})

	t.Run("listen host allowed", func(t *testing.T) {
		env := newTestEnvWithArtAndOptions(t, nil, Options{ListenHost: "coda.example.com"})
		req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
		req.Host = "coda.example.com:8687"
		w := httptest.NewRecorder()
		env.router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("listen host = %d, want 200", w.Code)
		}
	})
}

// TestAPIBodyLimit checks oversized JSON is rejected with 413 for both
// declared and chunked bodies.
func TestAPIBodyLimit(t *testing.T) {
	newOversizedJSON := func(n int) io.Reader {
		// {"id":"<n x's>"} well over 1 MiB.
		return bytes.NewReader(append(
			append([]byte(`{"id":"`), bytes.Repeat([]byte("x"), n)...),
			[]byte(`"}`)...,
		))
	}

	t.Run("declared length", func(t *testing.T) {
		env := newTestEnv(t)
		body := newOversizedJSON(2 << 20)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/artists", body)
		req.Host = "localhost"
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		env.router.ServeHTTP(w, req)
		if w.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("oversized = %d, want 413", w.Code)
		}
		if !strings.Contains(w.Body.String(), "PAYLOAD_TOO_LARGE") {
			t.Errorf("body = %q, want PAYLOAD_TOO_LARGE", w.Body.String())
		}
	})

	t.Run("chunked", func(t *testing.T) {
		env := newTestEnv(t)
		body := newOversizedJSON(2 << 20)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/artists", body)
		req.Host = "localhost"
		req.Header.Set("Content-Type", "application/json")
		// Unknown length forces streaming through MaxBytesReader.
		req.ContentLength = -1
		w := httptest.NewRecorder()
		env.router.ServeHTTP(w, req)
		if w.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("chunked oversized = %d, want 413", w.Code)
		}
	})

	t.Run("cover uploads keep 15MB limit", func(t *testing.T) {
		env := newTestEnv(t)
		seedUploadRelease(t, env)
		// 2 MiB valid PNG-signature payload: over the 1 MiB API cap but
		// well under the 15 MiB cover cap. Must not be rejected as 413.
		big := make([]byte, 2<<20)
		copy(big, []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A})
		w := doRawRequest(t, env, "PUT", "/api/v1/releases/"+uploadRelID+"/cover", big, "image/png")
		if w.Code == http.StatusRequestEntityTooLarge {
			t.Fatalf("cover 2MiB = 413, want upload path exempt from API cap")
		}
		// Oversized cover still 400 with the friendly limit message.
		huge := make([]byte, artwork.DefaultMaxBytes+1)
		copy(huge, []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A})
		w = doRawRequest(t, env, "PUT", "/api/v1/releases/"+uploadRelID+"/cover", huge, "image/png")
		if w.Code != http.StatusBadRequest {
			t.Fatalf("oversized cover = %d, want 400", w.Code)
		}
	})
}
