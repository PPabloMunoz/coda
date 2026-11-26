package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/ppablomunoz/coda/internal/artwork"
	"github.com/ppablomunoz/coda/internal/store"
)

// tinyPNG is a valid 1x1 PNG; the handler sniffs magic bytes, so the body
// must be a real image, not arbitrary bytes.
var tinyPNG = mustDecodeB64("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==")

func mustDecodeB64(s string) []byte {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

const (
	uploadArtistID = "d5b32d03-2aaf-4baf-ba5d-3721d095b833"
	uploadGroupID  = "11111111-2222-4333-8444-555555555555"
	uploadRelID    = "33333333-4444-4555-b666-777777777777"
	uploadTrackID  = "55555555-6666-4777-b888-999999999999"
)

func seedUploadRelease(t *testing.T, env *testEnv) {
	t.Helper()
	if err := env.st.CreateArtist(context.Background(), store.Artist{ID: uploadArtistID, Name: "Upload Artist"}); err != nil {
		t.Fatalf("CreateArtist = %v", err)
	}
	credit := []store.CreditEntry{{ArtistID: uploadArtistID, Name: "Upload Artist", CreditedName: "Upload Artist"}}
	if err := env.st.SaveDiscography(context.Background(), []store.FullReleaseParams{{
		ID:     uploadRelID,
		Title:  "Upload Release",
		Credit: credit,
		Group: store.ReleaseGroupParams{
			ID: uploadGroupID, Title: "Upload Group", Type: "album", Credit: credit,
		},
		Tracks: []store.TrackParams{{
			ID: uploadTrackID, ReleaseID: uploadRelID,
			Disc: 1, Position: 1, Number: "1", Title: "Upload Track",
			Credit:      credit,
			RecordingID: "77777777-8888-4999-baaa-bbbbbbbbbbbb",
		}},
	}}); err != nil {
		t.Fatalf("SaveDiscography = %v", err)
	}
}

func doRawRequest(t *testing.T, env *testEnv, method, path string, body []byte, contentType string) *httptest.ResponseRecorder {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rdr)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	w := httptest.NewRecorder()
	env.router.ServeHTTP(w, req)
	return w
}

func decodeCoverRow(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var row map[string]any
	if err := json.Unmarshal(body, &row); err != nil {
		t.Fatalf("decode cover row = %v (%q)", err, body)
	}
	return row
}

func TestUploadReleaseCoverRoundTrip(t *testing.T) {
	env := newTestEnv(t)
	seedUploadRelease(t, env)

	w := doRawRequest(t, env, "PUT", "/api/v1/releases/"+uploadRelID+"/cover", tinyPNG, "image/png")
	if w.Code != http.StatusOK {
		t.Fatalf("upload = %d %q, want 200", w.Code, w.Body.String())
	}
	row := decodeCoverRow(t, w.Body.Bytes())
	if row["source"] != "upload" {
		t.Errorf("source = %v, want upload", row["source"])
	}
	if row["mime"] != "image/png" {
		t.Errorf("mime = %v, want image/png", row["mime"])
	}
	if row["release_id"] != uploadRelID {
		t.Errorf("release_id = %v, want %q", row["release_id"], uploadRelID)
	}

	w = doRequest(t, env.router, "GET", "/api/v1/releases/"+uploadRelID+"/cover", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("get after upload = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("content-type = %q, want image/png", ct)
	}
	if !bytes.Equal(w.Body.Bytes(), tinyPNG) {
		t.Errorf("get bytes differ from uploaded (%d vs %d bytes)", w.Body.Len(), len(tinyPNG))
	}
}

func TestUploadReleaseCoverRejectsNonImage(t *testing.T) {
	env := newTestEnv(t)
	seedUploadRelease(t, env)

	w := doRawRequest(t, env, "PUT", "/api/v1/releases/"+uploadRelID+"/cover", []byte("hello, not an image"), "text/plain")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("non-image = %d, want 400", w.Code)
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("INVALID_INPUT")) {
		t.Errorf("body = %q, want INVALID_INPUT", w.Body.String())
	}
}

func TestUploadReleaseCoverRejectsEmpty(t *testing.T) {
	env := newTestEnv(t)
	seedUploadRelease(t, env)

	w := doRawRequest(t, env, "PUT", "/api/v1/releases/"+uploadRelID+"/cover", nil, "image/png")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("empty = %d, want 400", w.Code)
	}
}

func TestUploadReleaseCoverUnknownRelease(t *testing.T) {
	env := newTestEnv(t)

	w := doRawRequest(t, env, "PUT", "/api/v1/releases/00000000-0000-4000-8000-000000000000/cover", tinyPNG, "image/png")
	if w.Code != http.StatusNotFound {
		t.Fatalf("unknown = %d, want 404", w.Code)
	}
}

func TestUploadReleaseCoverRejectsOversized(t *testing.T) {
	env := newTestEnv(t)
	seedUploadRelease(t, env)

	big := make([]byte, artwork.DefaultMaxBytes+1)
	copy(big, []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A})
	w := doRawRequest(t, env, "PUT", "/api/v1/releases/"+uploadRelID+"/cover", big, "image/png")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("oversized = %d, want 400", w.Code)
	}
}

func TestUploadArtistCoverRoundTrip(t *testing.T) {
	env := newTestEnv(t)
	seedUploadRelease(t, env)

	w := doRawRequest(t, env, "PUT", "/api/v1/artists/"+uploadArtistID+"/cover", tinyPNG, "image/png")
	if w.Code != http.StatusOK {
		t.Fatalf("upload = %d %q, want 200", w.Code, w.Body.String())
	}
	row := decodeCoverRow(t, w.Body.Bytes())
	if row["source"] != "upload" {
		t.Errorf("source = %v, want upload", row["source"])
	}
	if row["artist_id"] != uploadArtistID {
		t.Errorf("artist_id = %v, want %q", row["artist_id"], uploadArtistID)
	}

	w = doRequest(t, env.router, "GET", "/api/v1/artists/"+uploadArtistID+"/cover", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("get after upload = %d, want 200", w.Code)
	}
	if !bytes.Equal(w.Body.Bytes(), tinyPNG) {
		t.Errorf("get bytes differ from uploaded")
	}
}

func TestUploadArtistCoverUnknownArtist(t *testing.T) {
	env := newTestEnv(t)

	w := doRawRequest(t, env, "PUT", "/api/v1/artists/00000000-0000-4000-8000-000000000000/cover", tinyPNG, "image/png")
	if w.Code != http.StatusNotFound {
		t.Fatalf("unknown = %d, want 404", w.Code)
	}
}

func TestDeleteReleaseCoverRoundTrip(t *testing.T) {
	env := newTestEnv(t)
	seedUploadRelease(t, env)

	w := doRawRequest(t, env, "PUT", "/api/v1/releases/"+uploadRelID+"/cover", tinyPNG, "image/png")
	if w.Code != http.StatusOK {
		t.Fatalf("upload = %d, want 200", w.Code)
	}

	w = doRawRequest(t, env, "DELETE", "/api/v1/releases/"+uploadRelID+"/cover", nil, "")
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete = %d %q, want 204", w.Code, w.Body.String())
	}

	// The cached file is gone and the GET stays 404 (no lazy refetch).
	entries, err := os.ReadDir(env.coverDir)
	if err != nil {
		t.Fatalf("read cover dir = %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("cover dir holds %d files, want 0", len(entries))
	}
	w = doRequest(t, env.router, "GET", "/api/v1/releases/"+uploadRelID+"/cover", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("get after delete = %d, want 404", w.Code)
	}
	row, err := env.st.GetCover(context.Background(), uploadRelID)
	if err != nil {
		t.Fatalf("GetCover = %v", err)
	}
	if !row.Removed {
		t.Error("Removed = false, want tombstone kept")
	}

	// Idempotent: a second delete still answers 204.
	w = doRawRequest(t, env, "DELETE", "/api/v1/releases/"+uploadRelID+"/cover", nil, "")
	if w.Code != http.StatusNoContent {
		t.Fatalf("second delete = %d, want 204", w.Code)
	}

	// A new upload clears the removal and serves again.
	w = doRawRequest(t, env, "PUT", "/api/v1/releases/"+uploadRelID+"/cover", tinyPNG, "image/png")
	if w.Code != http.StatusOK {
		t.Fatalf("re-upload = %d, want 200", w.Code)
	}
	w = doRequest(t, env.router, "GET", "/api/v1/releases/"+uploadRelID+"/cover", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("get after re-upload = %d, want 200", w.Code)
	}
}

func TestDeleteReleaseCoverErrors(t *testing.T) {
	env := newTestEnv(t)

	w := doRawRequest(t, env, "DELETE", "/api/v1/releases/not-a-uuid/cover", nil, "")
	if w.Code != http.StatusBadRequest {
		t.Errorf("bad id = %d, want 400", w.Code)
	}
	w = doRawRequest(t, env, "DELETE", "/api/v1/releases/00000000-0000-4000-8000-000000000000/cover", nil, "")
	if w.Code != http.StatusNotFound {
		t.Errorf("unknown = %d, want 404", w.Code)
	}
}

func TestDeleteArtistCoverRoundTrip(t *testing.T) {
	env := newTestEnv(t)
	seedUploadRelease(t, env)

	w := doRawRequest(t, env, "PUT", "/api/v1/artists/"+uploadArtistID+"/cover", tinyPNG, "image/png")
	if w.Code != http.StatusOK {
		t.Fatalf("upload = %d, want 200", w.Code)
	}

	w = doRawRequest(t, env, "DELETE", "/api/v1/artists/"+uploadArtistID+"/cover", nil, "")
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete = %d %q, want 204", w.Code, w.Body.String())
	}

	entries, err := os.ReadDir(env.coverDir)
	if err != nil {
		t.Fatalf("read cover dir = %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("cover dir holds %d files, want 0", len(entries))
	}
	w = doRequest(t, env.router, "GET", "/api/v1/artists/"+uploadArtistID+"/cover", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("get after delete = %d, want 404", w.Code)
	}

	w = doRawRequest(t, env, "DELETE", "/api/v1/artists/00000000-0000-4000-8000-000000000000/cover", nil, "")
	if w.Code != http.StatusNotFound {
		t.Errorf("unknown = %d, want 404", w.Code)
	}
}
