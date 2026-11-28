package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ppablomunoz/coda/internal/artwork"
)

func newURLTestArt(t *testing.T, maxBytes int64) *artwork.Fetcher {
	t.Helper()
	cfg := artwork.Config{UserAgent: "coda-test"}
	if maxBytes > 0 {
		cfg.MaxBytes = maxBytes
	}
	art, err := artwork.New(cfg)
	if err != nil {
		t.Fatalf("artwork.New = %v", err)
	}
	return art
}

func serveURLTestImages(t *testing.T, big []byte) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/cover.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(tinyPNG)
	})
	mux.HandleFunc("/page", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><body>not an image</body></html>"))
	})
	mux.HandleFunc("/big.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(big)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestUploadReleaseCoverFromURLRoundTrip(t *testing.T) {
	imgURL := serveURLTestImages(t, nil) + "/cover.png"
	env := newTestEnvWithArt(t, newURLTestArt(t, 0))
	seedUploadRelease(t, env)

	w := doRequest(t, env.router, "POST", "/api/v1/releases/"+uploadRelID+"/cover/url",
		map[string]string{"url": imgURL})
	if w.Code != http.StatusOK {
		t.Fatalf("url upload = %d %q, want 200", w.Code, w.Body.String())
	}
	row := decodeCoverRow(t, w.Body.Bytes())
	if row["source"] != "upload" {
		t.Errorf("source = %v, want upload", row["source"])
	}
	if row["source_url"] != imgURL {
		t.Errorf("source_url = %v, want %q", row["source_url"], imgURL)
	}

	w = doRequest(t, env.router, "GET", "/api/v1/releases/"+uploadRelID+"/cover", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("get after url upload = %d, want 200", w.Code)
	}
	if !bytes.Equal(w.Body.Bytes(), tinyPNG) {
		t.Error("get bytes differ from URL-fetched image")
	}
}

func TestUploadArtistCoverFromURLRoundTrip(t *testing.T) {
	imgURL := serveURLTestImages(t, nil) + "/cover.png"
	env := newTestEnvWithArt(t, newURLTestArt(t, 0))
	seedUploadRelease(t, env)

	w := doRequest(t, env.router, "POST", "/api/v1/artists/"+uploadArtistID+"/cover/url",
		map[string]string{"url": imgURL})
	if w.Code != http.StatusOK {
		t.Fatalf("url upload = %d %q, want 200", w.Code, w.Body.String())
	}
	row := decodeCoverRow(t, w.Body.Bytes())
	if row["source"] != "upload" {
		t.Errorf("source = %v, want upload", row["source"])
	}
	if row["source_url"] != imgURL {
		t.Errorf("source_url = %v, want %q", row["source_url"], imgURL)
	}
	if row["artist_id"] != uploadArtistID {
		t.Errorf("artist_id = %v, want %q", row["artist_id"], uploadArtistID)
	}

	w = doRequest(t, env.router, "GET", "/api/v1/artists/"+uploadArtistID+"/cover", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("get after url upload = %d, want 200", w.Code)
	}
	if !bytes.Equal(w.Body.Bytes(), tinyPNG) {
		t.Error("get bytes differ from URL-fetched image")
	}
}

func TestUploadCoverFromURLUnknownEntity(t *testing.T) {
	imgURL := serveURLTestImages(t, nil) + "/cover.png"
	env := newTestEnvWithArt(t, newURLTestArt(t, 0))
	body := map[string]string{"url": imgURL}

	w := doRequest(t, env.router, "POST", "/api/v1/releases/00000000-0000-4000-8000-000000000000/cover/url", body)
	if w.Code != http.StatusNotFound {
		t.Fatalf("unknown release = %d, want 404", w.Code)
	}
	w = doRequest(t, env.router, "POST", "/api/v1/artists/00000000-0000-4000-8000-000000000000/cover/url", body)
	if w.Code != http.StatusNotFound {
		t.Fatalf("unknown artist = %d, want 404", w.Code)
	}
}

func TestUploadCoverFromURLRejectsBadInput(t *testing.T) {
	base := serveURLTestImages(t, nil)
	env := newTestEnvWithArt(t, newURLTestArt(t, 0))
	seedUploadRelease(t, env)

	bodies := []any{
		map[string]string{"url": "ftp://example.com/cover.png"},
		map[string]string{"url": "not a url"},
		map[string]string{"url": "   "},
		map[string]string{},
	}
	for i, body := range bodies {
		w := doRequest(t, env.router, "POST", "/api/v1/releases/"+uploadRelID+"/cover/url", body)
		if w.Code != http.StatusBadRequest {
			t.Errorf("bad body %d = %d, want 400", i, w.Code)
		}
		if !bytes.Contains(w.Body.Bytes(), []byte("INVALID_INPUT")) {
			t.Errorf("bad body %d = %q, want INVALID_INPUT", i, w.Body.String())
		}
	}

	w := doRequest(t, env.router, "POST", "/api/v1/artists/"+uploadArtistID+"/cover/url",
		map[string]string{"url": base + "/page"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("non-image = %d, want 400", w.Code)
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("INVALID_INPUT")) {
		t.Errorf("non-image body = %q, want INVALID_INPUT", w.Body.String())
	}
}

func TestUploadCoverFromURLRejectsOversized(t *testing.T) {
	big := make([]byte, 100)
	copy(big, []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A})
	base := serveURLTestImages(t, big)
	env := newTestEnvWithArt(t, newURLTestArt(t, 16))
	seedUploadRelease(t, env)

	w := doRequest(t, env.router, "POST", "/api/v1/releases/"+uploadRelID+"/cover/url",
		map[string]string{"url": base + "/big.png"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("oversized = %d, want 400", w.Code)
	}
}

func TestUploadCoverFromURLFetcherDisabled(t *testing.T) {
	env := newTestEnv(t) // nil art disables provider fetching.
	seedUploadRelease(t, env)

	w := doRequest(t, env.router, "POST", "/api/v1/releases/"+uploadRelID+"/cover/url",
		map[string]string{"url": "https://example.com/cover.png"})
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("disabled fetcher = %d, want 500", w.Code)
	}
	w = doRequest(t, env.router, "POST", "/api/v1/artists/"+uploadArtistID+"/cover/url",
		map[string]string{"url": "https://example.com/photo.png"})
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("disabled fetcher = %d, want 500", w.Code)
	}
}
