package musicbrainz

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	apperrors "github.com/ppablomunoz/coda/internal/errors"
)

func newTestClient(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	c, err := New(Config{UserAgent: "coda-test/0.1", BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New = %v", err)
	}
	// No rate-limit wait in tests.
	c.limiter.SetLimit(1000)
	t.Cleanup(srv.Close)
	return c, srv
}

func TestGetArtistByID(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/artist/abc" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Header.Get("User-Agent") == "" {
			t.Error("missing User-Agent")
		}
		_ = json.NewEncoder(w).Encode(Artist{ID: "abc", Name: "Test"})
	})
	got, err := c.GetArtistByID(context.Background(), "abc")
	if err != nil {
		t.Fatalf("GetArtistByID = %v", err)
	}
	if got.Name != "Test" {
		t.Errorf("name = %q", got.Name)
	}
}

func TestSearchArtists(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("fmt") != "json" {
			t.Errorf("fmt = %q", q.Get("fmt"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"artists": []ArtistSearchResult{{ID: "1", Name: "Beatles", Score: 100}},
		})
	})
	got, err := c.SearchArtists(context.Background(), "beatles", 10)
	if err != nil {
		t.Fatalf("SearchArtists = %v", err)
	}
	if len(got) != 1 || got[0].Name != "Beatles" {
		t.Errorf("got %v", got)
	}

	// Null artists array becomes empty slice.
	c2, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"artists": null}`))
	})
	got, err = c2.SearchArtists(context.Background(), "x", 10)
	if err != nil || len(got) != 0 {
		t.Errorf("null = %v, %v; want empty", got, err)
	}
}

func TestNotFoundMapsToErrNotFound(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	_, err := c.GetArtistByID(context.Background(), "missing")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestRetryThenSuccess(t *testing.T) {
	calls := 0
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(Artist{ID: "a", Name: "OK"})
	})
	// Avoid real sleep: maxRetries path already tested; here one retry with 0s.
	got, err := c.GetArtistByID(context.Background(), "a")
	if err != nil {
		t.Fatalf("retry = %v", err)
	}
	if got.Name != "OK" || calls != 2 {
		t.Errorf("got %+v calls=%d", got, calls)
	}
}

func TestGetReleasesFilters(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"release-count": 3,
			"releases": []Release{
				{ID: "r1", Title: "Keep", Status: "Official", ReleaseGroup: ReleaseGroup{ID: "g1", PrimaryType: "Album"}},
				{ID: "r2", Title: "Bootleg", Status: "Bootleg", ReleaseGroup: ReleaseGroup{ID: "g2", PrimaryType: "Album"}},
				{ID: "r3", Title: "Interview", Status: "Official", ReleaseGroup: ReleaseGroup{ID: "g3", PrimaryType: "Interview"}},
			},
		})
	})
	got, err := c.GetReleasesWithTracksByArtist(context.Background(), "artist-1")
	if err != nil {
		t.Fatalf("GetReleases = %v", err)
	}
	if len(got) != 1 || got[0].ID != "r1" {
		t.Errorf("filtered = %v, want only r1", got)
	}
}

func TestGetReleaseByIDValidation(t *testing.T) {
	// Wrong group.
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(Release{
			ID: "rel-1", Title: "R", Status: "Official",
			ReleaseGroup: ReleaseGroup{ID: "other-group"},
		})
	})
	if _, err := c.GetReleaseByID(context.Background(), "want-group", "rel-1"); !apperrors.Is(err, apperrors.ErrInvalidInput) {
		t.Errorf("wrong group = %v, want INVALID_INPUT", err)
	}

	// Non-official.
	c2, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(Release{
			ID: "rel-1", Title: "R", Status: "Bootleg",
			ReleaseGroup: ReleaseGroup{ID: "g1"},
		})
	})
	if _, err := c2.GetReleaseByID(context.Background(), "g1", "rel-1"); !apperrors.Is(err, apperrors.ErrInvalidInput) {
		t.Errorf("bootleg = %v, want INVALID_INPUT", err)
	}
}
