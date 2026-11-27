package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	apperrors "github.com/ppablomunoz/coda/internal/errors"
)

// countingCoverServer counts every provider request and answers 404 or 200
// with a valid PNG on the CAA path.
func countingCoverServer(t *testing.T, calls *atomic.Int64, withArt bool) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if withArt && len(r.URL.Path) >= len("/release/") && r.URL.Path[:len("/release/")] == "/release/" {
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(tinyPNG)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestReleaseCoverMissCachedEndToEnd(t *testing.T) {
	var calls atomic.Int64
	srv := countingCoverServer(t, &calls, false)
	env := newTestEnvWithArt(t, newCoverTestFetcher(t, srv.URL))
	seedUploadRelease(t, env)

	w := doRequest(t, env.router, "GET", "/api/v1/releases/"+uploadRelID+"/cover", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("first get = %d, want 404", w.Code)
	}
	first := calls.Load()
	if first == 0 {
		t.Fatal("first miss made no provider calls")
	}
	if _, err := env.st.GetReleaseCoverMiss(context.Background(), uploadRelID); err != nil {
		t.Fatalf("miss row = %v, want recorded", err)
	}

	w = doRequest(t, env.router, "GET", "/api/v1/releases/"+uploadRelID+"/cover", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("second get = %d, want 404", w.Code)
	}
	if got := calls.Load(); got != first {
		t.Errorf("provider calls after cached miss = %d, want %d", got, first)
	}
}

func TestArtistCoverMissCachedEndToEnd(t *testing.T) {
	var calls atomic.Int64
	srv := countingCoverServer(t, &calls, false)
	env := newTestEnvWithArt(t, newCoverTestFetcher(t, srv.URL))
	seedUploadRelease(t, env)

	w := doRequest(t, env.router, "GET", "/api/v1/artists/"+uploadArtistID+"/cover", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("first get = %d, want 404", w.Code)
	}
	first := calls.Load()
	if first == 0 {
		t.Fatal("first artist miss made no provider calls")
	}

	w = doRequest(t, env.router, "GET", "/api/v1/artists/"+uploadArtistID+"/cover", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("second get = %d, want 404", w.Code)
	}
	if got := calls.Load(); got != first {
		t.Errorf("artist provider calls after cached miss = %d, want %d", got, first)
	}
}

func TestTombstonedCoverMakesNoProviderCalls(t *testing.T) {
	var calls atomic.Int64
	// Upstream has art: without the tombstone the GET would fetch and serve
	// 200, so zero calls proves the tombstone short-circuits providers.
	srv := countingCoverServer(t, &calls, true)
	env := newTestEnvWithArt(t, newCoverTestFetcher(t, srv.URL))
	seedUploadRelease(t, env)
	saveServerUploadCover(t, env)

	w := doRawRequest(t, env, "DELETE", "/api/v1/releases/"+uploadRelID+"/cover", nil, "")
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete = %d, want 204", w.Code)
	}
	if _, err := env.st.GetReleaseCoverMiss(context.Background(), uploadRelID); !apperrors.Is(err, apperrors.ErrNotFound) {
		t.Fatalf("miss after delete = %v, want cleared (tombstone wins)", err)
	}

	w = doRequest(t, env.router, "GET", "/api/v1/releases/"+uploadRelID+"/cover", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("get after delete = %d, want 404", w.Code)
	}
	if got := calls.Load(); got != 0 {
		t.Errorf("provider calls for tombstoned cover = %d, want 0", got)
	}

	// Same for artist photos.
	w = doRawRequest(t, env, "PUT", "/api/v1/artists/"+uploadArtistID+"/cover", tinyPNG, "image/png")
	if w.Code != http.StatusOK {
		t.Fatalf("artist upload = %d, want 200", w.Code)
	}
	w = doRawRequest(t, env, "DELETE", "/api/v1/artists/"+uploadArtistID+"/cover", nil, "")
	if w.Code != http.StatusNoContent {
		t.Fatalf("artist delete = %d, want 204", w.Code)
	}
	before := calls.Load()
	w = doRequest(t, env.router, "GET", "/api/v1/artists/"+uploadArtistID+"/cover", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("artist get after delete = %d, want 404", w.Code)
	}
	if got := calls.Load(); got != before {
		t.Errorf("artist provider calls for tombstone = %d, want %d", got, before)
	}
}

func TestReleaseCoverMissExpiresEndToEnd(t *testing.T) {
	var calls atomic.Int64
	srv := countingCoverServer(t, &calls, false)
	env := newTestEnvWithArt(t, newCoverTestFetcher(t, srv.URL))
	seedUploadRelease(t, env)

	w := doRequest(t, env.router, "GET", "/api/v1/releases/"+uploadRelID+"/cover", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("first get = %d, want 404", w.Code)
	}
	first := calls.Load()

	// Expire the miss directly and confirm the next GET refetches.
	past := time.Now().UTC().Add(-time.Hour)
	if err := env.st.UpsertReleaseCoverMiss(context.Background(), uploadRelID, 1, past, past); err != nil {
		t.Fatalf("expire miss = %v", err)
	}
	w = doRequest(t, env.router, "GET", "/api/v1/releases/"+uploadRelID+"/cover", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("get after expiry = %d, want 404", w.Code)
	}
	if got := calls.Load(); got <= first {
		t.Errorf("provider calls after expiry = %d, want > %d", got, first)
	}
}
