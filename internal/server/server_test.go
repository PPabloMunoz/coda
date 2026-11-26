package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/ppablomunoz/coda/internal/musicbrainz"
	"github.com/ppablomunoz/coda/internal/store"
	"github.com/ppablomunoz/coda/internal/tasks"
	"github.com/ppablomunoz/coda/internal/utils"
)

func init() { gin.SetMode(gin.TestMode) }

type testEnv struct {
	router *gin.Engine
	st     *store.Store
	queue  *tasks.Queue
	mbSrv  *httptest.Server
	mbMux  *http.ServeMux
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	log := utils.NewWithWriter(io.Discard)
	st, err := store.NewStore(filepath.Join(t.TempDir(), "test.db"), nil, log)
	if err != nil {
		t.Fatalf("NewStore = %v", err)
	}
	t.Cleanup(st.Close)
	q := tasks.NewQueue(log)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	mb, err := musicbrainz.New(musicbrainz.Config{UserAgent: "coda-test", BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("mb New = %v", err)
	}
	router := NewHandler(log, st, mb, q, nil, "")
	return &testEnv{router: router, st: st, queue: q, mbSrv: srv, mbMux: mux}
}

func doRequest(t *testing.T, router *gin.Engine, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal = %v", err)
		}
		rdr = bytes.NewReader(buf)
	}
	req := httptest.NewRequest(method, path, rdr)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func TestHealth(t *testing.T) {
	env := newTestEnv(t)
	w := doRequest(t, env.router, "GET", "/api/v1/health", nil)
	if w.Code != http.StatusOK || w.Body.String() != "ok" {
		t.Errorf("health = %d %q", w.Code, w.Body.String())
	}
}

func TestPostScan(t *testing.T) {
	env := newTestEnv(t)
	w := doRequest(t, env.router, "POST", "/api/v1/scan", nil)
	if w.Code != http.StatusAccepted {
		t.Errorf("scan = %d, want 202", w.Code)
	}
}

func TestParseLimitAndOffset(t *testing.T) {
	env := newTestEnv(t)
	w := doRequest(t, env.router, "GET", "/api/v1/artists?limit=0", nil)
	if w.Code != http.StatusBadRequest {
		t.Errorf("limit=0 = %d, want 400", w.Code)
	}
	w = doRequest(t, env.router, "GET", "/api/v1/artists?limit=abc", nil)
	if w.Code != http.StatusBadRequest {
		t.Errorf("limit=abc = %d, want 400", w.Code)
	}
	w = doRequest(t, env.router, "GET", "/api/v1/artists?offset=-1", nil)
	if w.Code != http.StatusBadRequest {
		t.Errorf("offset=-1 = %d, want 400", w.Code)
	}
	w = doRequest(t, env.router, "GET", "/api/v1/artists?limit=5&offset=0", nil)
	if w.Code != http.StatusOK {
		t.Errorf("valid = %d, want 200", w.Code)
	}
}

func TestRequireHelpers(t *testing.T) {
	if _, err := requireMBID("not-a-uuid"); err == nil {
		t.Error("bad MBID = nil")
	}
	if _, err := requireLocalFileID("0"); err == nil {
		t.Error("file id 0 = nil")
	}
	if _, err := requireLocalFileID("abc"); err == nil {
		t.Error("file id abc = nil")
	}
	if s, err := requireOptionalMBID(""); err != nil || s != "" {
		t.Errorf("optional empty = %q, %v", s, err)
	}
	if _, err := requireOptionalMBID("bad"); err == nil {
		t.Error("optional bad = nil")
	}
	if b, err := parseOptionalBool(""); err != nil || b != nil {
		t.Errorf("optional bool empty = %v, %v", b, err)
	}
	if _, err := parseOptionalBool("maybe"); err == nil {
		t.Error("optional bool maybe = nil")
	}
}

func TestHttpStatusFor(t *testing.T) {
	cases := map[string]int{
		"NOT_FOUND": 404, "UNAUTHORIZED": 401, "FORBIDDEN": 403,
		"ALREADY_EXISTS": 409, "INVALID_INPUT": 400, "INVALID_ID": 400,
		"MUSICBRAINZ_FAILED": 502, "UNKNOWN": 500, "": 500,
	}
	for code, want := range cases {
		if got := httpStatusFor(code); got != want {
			t.Errorf("%q = %d, want %d", code, got, want)
		}
	}
}

func TestGetArtistErrors(t *testing.T) {
	env := newTestEnv(t)
	w := doRequest(t, env.router, "GET", "/api/v1/artists/not-uuid", nil)
	if w.Code != http.StatusBadRequest {
		t.Errorf("bad id = %d, want 400", w.Code)
	}
	w = doRequest(t, env.router, "GET", "/api/v1/artists/d5b32d03-2aaf-4baf-ba5d-3721d095b833", nil)
	if w.Code != http.StatusNotFound {
		t.Errorf("missing = %d, want 404", w.Code)
	}
}

func TestSearchMusicBrainzValidation(t *testing.T) {
	env := newTestEnv(t)
	w := doRequest(t, env.router, "GET", "/api/v1/musicbrainz/artists?q=a", nil)
	if w.Code != http.StatusBadRequest {
		t.Errorf("short q = %d, want 400", w.Code)
	}
	w = doRequest(t, env.router, "GET", "/api/v1/musicbrainz/artists?q=ab&limit=99", nil)
	if w.Code != http.StatusBadRequest {
		t.Errorf("bad limit = %d, want 400", w.Code)
	}
}

func TestSearchMusicBrainzSuccess(t *testing.T) {
	env := newTestEnv(t)
	env.mbMux.HandleFunc("/artist", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"artists": []any{map[string]any{"id": "1", "name": "Beatles", "score": 100}},
		})
	})
	w := doRequest(t, env.router, "GET", "/api/v1/musicbrainz/artists?q=beatles", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("search = %d %s", w.Code, w.Body.String())
	}
	var resp Response[[]musicbrainz.ArtistSearchResult]
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode = %v", err)
	}
	if resp.Total != 1 {
		t.Errorf("total = %d, want 1", resp.Total)
	}
}

func TestGetReleaseGroupsValidation(t *testing.T) {
	env := newTestEnv(t)
	w := doRequest(t, env.router, "GET", "/api/v1/release-groups?type=bogus", nil)
	if w.Code != http.StatusBadRequest {
		t.Errorf("bad type = %d, want 400", w.Code)
	}
	w = doRequest(t, env.router, "GET", "/api/v1/release-groups?primary=true", nil)
	if w.Code != http.StatusBadRequest {
		t.Errorf("primary w/o artist = %d, want 400", w.Code)
	}
	w = doRequest(t, env.router, "GET", "/api/v1/release-groups?artist=bad", nil)
	if w.Code != http.StatusBadRequest {
		t.Errorf("bad artist = %d, want 400", w.Code)
	}
	w = doRequest(t, env.router, "GET", "/api/v1/release-groups?limit=5", nil)
	if w.Code != http.StatusOK {
		t.Errorf("valid = %d, want 200", w.Code)
	}
}

func TestGetReleasesValidation(t *testing.T) {
	env := newTestEnv(t)
	w := doRequest(t, env.router, "GET", "/api/v1/releases", nil)
	if w.Code != http.StatusBadRequest {
		t.Errorf("missing group = %d, want 400", w.Code)
	}
	w = doRequest(t, env.router, "GET", "/api/v1/releases?group=d5b32d03-2aaf-4baf-ba5d-3721d095b833", nil)
	if w.Code != http.StatusNotFound {
		t.Errorf("missing = %d, want 404", w.Code)
	}
}

func TestGetLocalFilesValidation(t *testing.T) {
	env := newTestEnv(t)
	w := doRequest(t, env.router, "GET", "/api/v1/files?linked=maybe", nil)
	if w.Code != http.StatusBadRequest {
		t.Errorf("bad linked = %d, want 400", w.Code)
	}
	w = doRequest(t, env.router, "GET", "/api/v1/files/abc", nil)
	if w.Code != http.StatusBadRequest {
		t.Errorf("bad id = %d, want 400", w.Code)
	}
	w = doRequest(t, env.router, "GET", "/api/v1/files/999", nil)
	if w.Code != http.StatusNotFound {
		t.Errorf("missing = %d, want 404", w.Code)
	}
	w = doRequest(t, env.router, "GET", "/api/v1/files?limit=5", nil)
	if w.Code != http.StatusOK {
		t.Errorf("valid = %d, want 200", w.Code)
	}
}

// TestPostLinkRejectsInvalidTrackID guards the link handler's report of a
// bad track_id: it must answer 400 with a JSON error, never an empty 200.
func TestPostLinkRejectsInvalidTrackID(t *testing.T) {
	env := newTestEnv(t)
	w := doRequest(t, env.router, "POST", "/api/v1/files/1/link",
		map[string]string{"track_id": "not-a-uuid"})
	if w.Code != http.StatusBadRequest {
		t.Errorf("bad track_id = %d, want 400", w.Code)
	}
	if !strings.Contains(w.Body.String(), "INVALID_ID") {
		t.Errorf("body = %q, want an INVALID_ID error object", w.Body.String())
	}
}

func TestLibraryStatsEmpty(t *testing.T) {
	env := newTestEnv(t)
	w := doRequest(t, env.router, "GET", "/api/v1/stats", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("stats = %d", w.Code)
	}
}

func TestPostArtistFlow(t *testing.T) {
	env := newTestEnv(t)
	id := "d5b32d03-2aaf-4baf-ba5d-3721d095b833"
	env.mbMux.HandleFunc("/artist/"+id, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(musicbrainz.Artist{ID: id, Name: "Test"})
	})

	// Invalid body.
	w := doRequest(t, env.router, "POST", "/api/v1/artists", map[string]string{"id": "bad"})
	if w.Code != http.StatusBadRequest {
		t.Errorf("bad id = %d, want 400", w.Code)
	}

	// Create → 201.
	w = doRequest(t, env.router, "POST", "/api/v1/artists", map[string]string{"id": id})
	if w.Code != http.StatusCreated {
		t.Fatalf("create = %d %s, want 201", w.Code, w.Body.String())
	}

	// Same id again → 304 (already in library, requeues refresh).
	w = doRequest(t, env.router, "POST", "/api/v1/artists", map[string]string{"id": id})
	if w.Code != http.StatusNotModified {
		t.Errorf("dup = %d, want 304", w.Code)
	}

	// Refresh → 202.
	w = doRequest(t, env.router, "POST", "/api/v1/artists/"+id+"/refresh", nil)
	if w.Code != http.StatusAccepted {
		t.Errorf("refresh = %d, want 202", w.Code)
	}

	// Import status → 200.
	w = doRequest(t, env.router, "GET", "/api/v1/artists/"+id+"/import", nil)
	if w.Code != http.StatusOK {
		t.Errorf("import status = %d, want 200", w.Code)
	}

	// Delete → 200, then get → 404.
	w = doRequest(t, env.router, "DELETE", "/api/v1/artists/"+id, nil)
	if w.Code != http.StatusOK {
		t.Errorf("delete = %d, want 200", w.Code)
	}
	w = doRequest(t, env.router, "GET", "/api/v1/artists/"+id, nil)
	if w.Code != http.StatusNotFound {
		t.Errorf("after delete = %d, want 404", w.Code)
	}
}

func TestDeleteReleaseGroupFlow(t *testing.T) {
	env := newTestEnv(t)
	w := doRequest(t, env.router, "DELETE", "/api/v1/release-groups/d5b32d03-2aaf-4baf-ba5d-3721d095b833", nil)
	if w.Code != http.StatusNotFound {
		t.Errorf("missing delete = %d, want 404", w.Code)
	}
	w = doRequest(t, env.router, "DELETE", "/api/v1/release-groups/bad", nil)
	if w.Code != http.StatusBadRequest {
		t.Errorf("bad id = %d, want 400", w.Code)
	}
}

func TestReplaceReleaseValidation(t *testing.T) {
	groupID := "d5b32d03-2aaf-4baf-ba5d-3721d095b833"
	otherRelease := "11111111-1111-1111-1111-111111111111"
	env := newTestEnv(t)
	env.mbMux.HandleFunc("/release/"+otherRelease, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(musicbrainz.Release{
			ID: otherRelease, Title: "Other", Status: "Official",
			ReleaseGroup: musicbrainz.ReleaseGroup{ID: "22222222-2222-2222-2222-222222222222"},
		})
	})
	w := doRequest(t, env.router, "PUT", "/api/v1/release-groups/"+groupID+"/update",
		map[string]string{"new_release_id": otherRelease})
	if w.Code != http.StatusBadRequest {
		t.Errorf("cross-group = %d, want 400", w.Code)
	}

	badMB := "33333333-3333-3333-3333-333333333333"
	env.mbMux.HandleFunc("/release/"+badMB, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	})
	w = doRequest(t, env.router, "PUT", "/api/v1/release-groups/"+groupID+"/update",
		map[string]string{"new_release_id": badMB})
	if w.Code != http.StatusBadGateway {
		t.Errorf("mb failure = %d, want 502", w.Code)
	}
}
