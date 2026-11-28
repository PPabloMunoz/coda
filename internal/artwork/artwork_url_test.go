package artwork

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apperrors "github.com/ppablomunoz/coda/internal/errors"
)

func newURLTestFetcher(t *testing.T, maxBytes int64) *Fetcher {
	t.Helper()
	cfg := Config{UserAgent: "coda-test"}
	if maxBytes > 0 {
		cfg.MaxBytes = maxBytes
	}
	f, err := New(cfg)
	if err != nil {
		t.Fatalf("New = %v", err)
	}
	return f
}

func TestFetchURLRoundTrip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(testPNG)
	}))
	t.Cleanup(srv.Close)

	f := newURLTestFetcher(t, 0)
	res, err := f.FetchURL(context.Background(), srv.URL+"/cover.png")
	if err != nil {
		t.Fatalf("FetchURL = %v, want success", err)
	}
	if string(res.Bytes) != string(testPNG) {
		t.Error("bytes differ from served image")
	}
	if res.MIME != "image/png" {
		t.Errorf("MIME = %q, want image/png", res.MIME)
	}
	if res.Source != SourceUpload {
		t.Errorf("Source = %q, want upload", res.Source)
	}
	if res.SourceURL != srv.URL+"/cover.png" {
		t.Errorf("SourceURL = %q, want the fetched URL", res.SourceURL)
	}
}

func TestFetchURLRejectsBadURLs(t *testing.T) {
	f := newURLTestFetcher(t, 0)
	for _, raw := range []string{
		"",
		"   ",
		"not a url",
		"ftp://example.com/cover.png",
		"file:///etc/passwd",
		"http://user:pass@example.com/cover.png",
		"http://",
		strings.Repeat("https://example.com/", 200),
	} {
		if _, err := f.FetchURL(context.Background(), raw); !apperrors.Is(err, apperrors.ErrInvalidInput) {
			t.Errorf("FetchURL(%q) = %v, want INVALID_INPUT", raw, err)
		}
	}
}

func TestFetchURLMapsContentFailuresToInvalidInput(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/missing.png", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("/page", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><body>not an image</body></html>"))
	})
	mux.HandleFunc("/empty.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	f := newURLTestFetcher(t, 0)
	for _, path := range []string{"/missing.png", "/page", "/empty.png"} {
		if _, err := f.FetchURL(context.Background(), srv.URL+path); !apperrors.Is(err, apperrors.ErrInvalidInput) {
			t.Errorf("FetchURL(%s) = %v, want INVALID_INPUT", path, err)
		}
	}
}

func TestFetchURLRejectsOversized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(testPNG)
	}))
	t.Cleanup(srv.Close)

	f := newURLTestFetcher(t, 16)
	if _, err := f.FetchURL(context.Background(), srv.URL+"/big.png"); !apperrors.Is(err, apperrors.ErrInvalidInput) {
		t.Errorf("FetchURL oversized = %v, want INVALID_INPUT", err)
	}
}

func TestFetchURLTransportFailureStaysInternal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL + "/cover.png"
	srv.Close() // refused connection: a server problem, not bad input.

	f := newURLTestFetcher(t, 0)
	_, err := f.FetchURL(context.Background(), url)
	if err == nil {
		t.Fatal("FetchURL = nil, want transport error")
	}
	if apperrors.Is(err, apperrors.ErrInvalidInput) {
		t.Errorf("FetchURL = %v, want internal, not INVALID_INPUT", err)
	}
	if !apperrors.Is(err, apperrors.ErrInternal) {
		t.Errorf("FetchURL = %v, want INTERNAL_ERROR", err)
	}
}
