package artwork

import (
	"bytes"
	"context"
	stderrors "errors"
	"fmt"
	"image"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	apperrors "github.com/ppablomunoz/coda/internal/errors"
)

const (
	providerArtist = "Blue Artist"
	providerAlbum  = "Blue Album"
	providerMBID   = "11111111-2222-4333-a444-555555555555"
)

// chainFetcher points every provider base at one test server.
func chainFetcher(t *testing.T, srvURL string) *Fetcher {
	t.Helper()
	f, err := New(Config{
		UserAgent:      "coda-test",
		CAABaseURL:     srvURL,
		DeezerBaseURL:  srvURL,
		ITunesBaseURL:  srvURL,
		CommonsBaseURL: srvURL,
		Timeout:        5000000000,
		FetchTimeout:   5000000000,
		Concurrency:    8,
	})
	if err != nil {
		t.Fatalf("New = %v", err)
	}
	return f
}

func servePNG(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/png")
	_, _ = w.Write(testPNG)
}

func TestNewValidationAndDefaults(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Error("New without UserAgent = nil, want error")
	}

	f, err := New(Config{UserAgent: "coda-test", CAABaseURL: "http://x/"})
	if err != nil {
		t.Fatalf("New = %v", err)
	}
	if f.caaBase != "http://x" {
		t.Errorf("caaBase = %q, want trimmed", f.caaBase)
	}
	if f.deezerBase != DefaultDeezerBaseURL {
		t.Errorf("deezerBase = %q, want default", f.deezerBase)
	}
	if f.itunesBase != DefaultiTunesBaseURL {
		t.Errorf("itunesBase = %q, want default", f.itunesBase)
	}
	if f.commonsBase != DefaultCommonsBaseURL {
		t.Errorf("commonsBase = %q, want default", f.commonsBase)
	}
	if f.maxBytes != DefaultMaxBytes {
		t.Errorf("maxBytes = %d, want %d", f.maxBytes, DefaultMaxBytes)
	}
	if f.fetchTimeout != DefaultFetchTimeout {
		t.Errorf("fetchTimeout = %s, want %s", f.fetchTimeout, DefaultFetchTimeout)
	}
	if cap(f.sem) != DefaultFetchConcurrency {
		t.Errorf("sem cap = %d, want %d", cap(f.sem), DefaultFetchConcurrency)
	}
}

func TestNormName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Sofía Cámara", "sofia camara"},
		{"  HELLO World  ", "hello world"},
		{"Björk", "bjork"},
		{"AC/DC", "ac/dc"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := normName(tc.in); got != tc.want {
			t.Errorf("normName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestIsDeezerPlaceholder(t *testing.T) {
	cases := []struct {
		url  string
		want bool
	}{
		// Empty hash segment: Deezer's blank mark.
		{"https://cdn.dzcdn.net/images/artist//abc/pic.jpg", true},
		// Explicit blank mark on a full path.
		{"https://e-cdns-images.dzcdn.net/images/cover/abc/000000-80-0-0.jpg", true},
		{"https://e-cdns-images.dzcdn.net/images/cover/abc/500x500-000000-80-0-0.jpg", true},
		{"https://e-cdns-images.dzcdn.net/images/cover/abc123/500x500.jpg", false},
		{"https://e-cdns-images.dzcdn.net/images/artist/pic-500x500.jpg", false},
		{"://bad", false},
	}
	for _, tc := range cases {
		if got := isDeezerPlaceholder(tc.url); got != tc.want {
			t.Errorf("isDeezerPlaceholder(%q) = %v, want %v", tc.url, got, tc.want)
		}
	}
}

func TestFirstUsable(t *testing.T) {
	placeholder := "https://cdn.dzcdn.net/images/cover//pic.jpg"
	good := "https://cdn.dzcdn.net/images/cover/abc/pic.jpg"
	if got := firstUsable("", placeholder, good); got != good {
		t.Errorf("firstUsable skips empty+placeholder = %q, want %q", got, good)
	}
	if got := firstUsable(good, "https://other/x.jpg"); got != good {
		t.Errorf("firstUsable prefers first = %q, want %q", got, good)
	}
	if got := firstUsable("", placeholder); got != "" {
		t.Errorf("firstUsable all-bad = %q, want empty", got)
	}
}

func TestFetchCAA(t *testing.T) {
	ctx := context.Background()

	t.Run("hit", func(t *testing.T) {
		var gotUA string
		mux := http.NewServeMux()
		mux.HandleFunc("/release/"+providerMBID+"/front", func(w http.ResponseWriter, r *http.Request) {
			gotUA = r.Header.Get("User-Agent")
			servePNG(w, r)
		})
		srv := httptest.NewServer(mux)
		t.Cleanup(srv.Close)

		f, err := New(Config{UserAgent: "coda-test", CAABaseURL: srv.URL})
		if err != nil {
			t.Fatal(err)
		}
		res, err := f.fetchCAA(ctx, providerMBID)
		if err != nil {
			t.Fatalf("fetchCAA = %v", err)
		}
		if res.Source != SourceCoverArtArchive {
			t.Errorf("source = %q, want %q", res.Source, SourceCoverArtArchive)
		}
		// The served PNG is normalized to JPEG on fetch.
		if res.MIME != "image/jpeg" {
			t.Errorf("mime = %q, want image/jpeg (normalized)", res.MIME)
		}
		if cfg, format, err := image.DecodeConfig(bytes.NewReader(res.Bytes)); err != nil {
			t.Errorf("DecodeConfig = %v", err)
		} else if format != "jpeg" || cfg.Width != 1 || cfg.Height != 1 {
			t.Errorf("normalized = %s %dx%d, want jpeg 1x1", format, cfg.Width, cfg.Height)
		}
		if gotUA != "coda-test" {
			t.Errorf("User-Agent = %q, want coda-test", gotUA)
		}
	})

	t.Run("not found", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		})
		srv := httptest.NewServer(mux)
		t.Cleanup(srv.Close)

		f, err := New(Config{UserAgent: "coda-test", CAABaseURL: srv.URL})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.fetchCAA(ctx, providerMBID); !apperrors.Is(err, apperrors.ErrNotFound) {
			t.Errorf("fetchCAA = %v, want NOT_FOUND", err)
		}
	})

	t.Run("upstream error surfaces", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})
		srv := httptest.NewServer(mux)
		t.Cleanup(srv.Close)

		f, err := New(Config{UserAgent: "coda-test", CAABaseURL: srv.URL})
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.fetchCAA(ctx, providerMBID)
		if err == nil {
			t.Fatal("fetchCAA = nil, want error")
		}
		if apperrors.Is(err, apperrors.ErrNotFound) {
			t.Errorf("fetchCAA = NOT_FOUND, want upstream error")
		}
	})
}

// deezerAlbumServer serves one /search/album payload plus the image URLs it
// names (all pointing back at the same server).
func deezerAlbumServer(t *testing.T, payload func(base string) string) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/search/album", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, payload(srv.URL))
	})
	mux.HandleFunc("/img/", servePNG)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestFetchDeezer(t *testing.T) {
	ctx := context.Background()
	newDeezerOnly := func(t *testing.T, srv *httptest.Server) *Fetcher {
		t.Helper()
		f, err := New(Config{UserAgent: "coda-test", DeezerBaseURL: srv.URL})
		if err != nil {
			t.Fatal(err)
		}
		return f
	}

	t.Run("exact match prefers largest rendition", func(t *testing.T) {
		srv := deezerAlbumServer(t, func(base string) string {
			return `{"data":[{"title":"` + providerAlbum + `","artist":{"name":"` + providerArtist +
				`"},"cover_xl":"` + base + `/img/xl.png","cover_big":"` + base + `/img/big.png"}]}`
		})
		res, err := newDeezerOnly(t, srv).fetchDeezer(ctx, providerArtist, providerAlbum)
		if err != nil {
			t.Fatalf("fetchDeezer = %v", err)
		}
		if res.Source != SourceDeezer {
			t.Errorf("source = %q, want deezer", res.Source)
		}
		if want := srv.URL + "/img/xl.png"; res.SourceURL != want {
			t.Errorf("sourceURL = %q, want %q (largest first)", res.SourceURL, want)
		}
	})

	t.Run("wrong title rejected", func(t *testing.T) {
		srv := deezerAlbumServer(t, func(base string) string {
			return `{"data":[{"title":"Wrong Album","artist":{"name":"` + providerArtist +
				`"},"cover_xl":"` + base + `/img/xl.png"}]}`
		})
		if _, err := newDeezerOnly(t, srv).fetchDeezer(ctx, providerArtist, providerAlbum); !apperrors.Is(err, apperrors.ErrNotFound) {
			t.Errorf("fetchDeezer = %v, want NOT_FOUND", err)
		}
	})

	t.Run("wrong artist rejected", func(t *testing.T) {
		srv := deezerAlbumServer(t, func(base string) string {
			return `{"data":[{"title":"` + providerAlbum + `","artist":{"name":"Stranger"},"cover_xl":"` + base + `/img/xl.png"}]}`
		})
		if _, err := newDeezerOnly(t, srv).fetchDeezer(ctx, providerArtist, providerAlbum); !apperrors.Is(err, apperrors.ErrNotFound) {
			t.Errorf("fetchDeezer = %v, want NOT_FOUND", err)
		}
	})

	t.Run("diacritics match", func(t *testing.T) {
		srv := deezerAlbumServer(t, func(base string) string {
			return `{"data":[{"title":"Sofia Camara","artist":{"name":"Sofia Camara"},"cover_xl":"` + base + `/img/xl.png"}]}`
		})
		// Query carries the accented form; the match must still hold so the
		// art is accepted instead of falling through to the next provider.
		if _, err := newDeezerOnly(t, srv).fetchDeezer(ctx, "Sofía Cámara", "Sofia Camara"); err != nil {
			t.Errorf("fetchDeezer = %v, want success (diacritic-insensitive)", err)
		}
	})

	t.Run("placeholder covers skipped", func(t *testing.T) {
		srv := deezerAlbumServer(t, func(base string) string {
			return `{"data":[{"title":"` + providerAlbum + `","artist":{"name":"` + providerArtist +
				`"},"cover_xl":"https://cdn.dzcdn.net/images/cover//pic.jpg","cover_big":"` + base +
				`/img/big.png","cover_medium":"","cover":""}]}`
		})
		res, err := newDeezerOnly(t, srv).fetchDeezer(ctx, providerArtist, providerAlbum)
		if err != nil {
			t.Fatalf("fetchDeezer = %v", err)
		}
		if want := srv.URL + "/img/big.png"; res.SourceURL != want {
			t.Errorf("sourceURL = %q, want %q (placeholder skipped)", res.SourceURL, want)
		}
	})

	t.Run("placeholder only is a miss", func(t *testing.T) {
		srv := deezerAlbumServer(t, func(base string) string {
			return `{"data":[{"title":"` + providerAlbum + `","artist":{"name":"` + providerArtist +
				`"},"cover_xl":"https://cdn.dzcdn.net/images/cover//pic.jpg"}]}`
		})
		if _, err := newDeezerOnly(t, srv).fetchDeezer(ctx, providerArtist, providerAlbum); !apperrors.Is(err, apperrors.ErrNotFound) {
			t.Errorf("fetchDeezer = %v, want NOT_FOUND", err)
		}
	})

	t.Run("upstream error surfaces", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
		})
		srv := httptest.NewServer(mux)
		t.Cleanup(srv.Close)

		_, err := newDeezerOnly(t, srv).fetchDeezer(ctx, providerArtist, providerAlbum)
		if err == nil {
			t.Fatal("fetchDeezer = nil, want error")
		}
		if apperrors.Is(err, apperrors.ErrNotFound) {
			t.Error("fetchDeezer = NOT_FOUND, want upstream error")
		}
	})

	t.Run("empty query is a miss without requests", func(t *testing.T) {
		var hits atomic.Int64
		mux := http.NewServeMux()
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			w.WriteHeader(http.StatusInternalServerError)
		})
		srv := httptest.NewServer(mux)
		t.Cleanup(srv.Close)

		if _, err := newDeezerOnly(t, srv).fetchDeezer(ctx, "", ""); !apperrors.Is(err, apperrors.ErrNotFound) {
			t.Errorf("fetchDeezer = %v, want NOT_FOUND", err)
		}
		if hits.Load() != 0 {
			t.Errorf("requests = %d, want 0 (no query, no call)", hits.Load())
		}
	})
}

func TestFetchDeezerArtist(t *testing.T) {
	ctx := context.Background()

	t.Run("exact match wins", func(t *testing.T) {
		var srv *httptest.Server
		mux := http.NewServeMux()
		mux.HandleFunc("/search/artist", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"data":[{"name":"%s","picture_xl":"%s/img/pic.png"},
				{"name":"Stranger","picture_xl":"%s/img/wrong.png"}]}`,
				providerArtist, srv.URL, srv.URL)
		})
		mux.HandleFunc("/img/", servePNG)
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		})
		srv = httptest.NewServer(mux)
		t.Cleanup(srv.Close)

		f, err := New(Config{UserAgent: "coda-test", DeezerBaseURL: srv.URL})
		if err != nil {
			t.Fatal(err)
		}
		res, err := f.fetchDeezerArtist(ctx, providerArtist)
		if err != nil {
			t.Fatalf("fetchDeezerArtist = %v", err)
		}
		if want := srv.URL + "/img/pic.png"; res.SourceURL != want {
			t.Errorf("sourceURL = %q, want %q", res.SourceURL, want)
		}
	})

	t.Run("same-name stranger rejected", func(t *testing.T) {
		var srv *httptest.Server
		mux := http.NewServeMux()
		mux.HandleFunc("/search/artist", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"data":[{"name":"Stranger","picture_xl":"%s/img/wrong.png"}]}`, srv.URL)
		})
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		})
		srv = httptest.NewServer(mux)
		t.Cleanup(srv.Close)

		f, err := New(Config{UserAgent: "coda-test", DeezerBaseURL: srv.URL})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.fetchDeezerArtist(ctx, providerArtist); !apperrors.Is(err, apperrors.ErrNotFound) {
			t.Errorf("fetchDeezerArtist = %v, want NOT_FOUND", err)
		}
	})

	t.Run("empty name is a miss without requests", func(t *testing.T) {
		var hits atomic.Int64
		mux := http.NewServeMux()
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			w.WriteHeader(http.StatusInternalServerError)
		})
		srv := httptest.NewServer(mux)
		t.Cleanup(srv.Close)

		f, err := New(Config{UserAgent: "coda-test", DeezerBaseURL: srv.URL})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.fetchDeezerArtist(ctx, "  "); !apperrors.Is(err, apperrors.ErrNotFound) {
			t.Errorf("fetchDeezerArtist = %v, want NOT_FOUND", err)
		}
		if hits.Load() != 0 {
			t.Errorf("requests = %d, want 0", hits.Load())
		}
	})
}

func TestFetchITunes(t *testing.T) {
	ctx := context.Background()
	newITunesOnly := func(t *testing.T, srv *httptest.Server) *Fetcher {
		t.Helper()
		f, err := New(Config{UserAgent: "coda-test", ITunesBaseURL: srv.URL})
		if err != nil {
			t.Fatal(err)
		}
		return f
	}

	t.Run("exact match upgrades to 600px", func(t *testing.T) {
		var srv *httptest.Server
		mux := http.NewServeMux()
		mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"resultCount":1,"results":[{"artistName":"%s",
				"collectionName":"%s","artworkUrl100":"%s/art/100x100bb.jpg"}]}`,
				providerArtist, providerAlbum, srv.URL)
		})
		mux.HandleFunc("/art/", servePNG)
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		})
		srv = httptest.NewServer(mux)
		t.Cleanup(srv.Close)

		res, err := newITunesOnly(t, srv).fetchiTunes(ctx, providerArtist, providerAlbum)
		if err != nil {
			t.Fatalf("fetchiTunes = %v", err)
		}
		if res.Source != SourceiTunes {
			t.Errorf("source = %q, want itunes", res.Source)
		}
		if want := srv.URL + "/art/600x600bb.jpg"; res.SourceURL != want {
			t.Errorf("sourceURL = %q, want %q (resized)", res.SourceURL, want)
		}
	})

	t.Run("wrong title rejected", func(t *testing.T) {
		var srv *httptest.Server
		mux := http.NewServeMux()
		mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"resultCount":1,"results":[{"artistName":"%s",
				"collectionName":"Wrong Album","artworkUrl600":"%s/art/a.jpg"}]}`,
				providerArtist, srv.URL)
		})
		srv = httptest.NewServer(mux)
		t.Cleanup(srv.Close)

		if _, err := newITunesOnly(t, srv).fetchiTunes(ctx, providerArtist, providerAlbum); !apperrors.Is(err, apperrors.ErrNotFound) {
			t.Errorf("fetchiTunes = %v, want NOT_FOUND", err)
		}
	})

	t.Run("rate limit surfaces as error", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		})
		srv := httptest.NewServer(mux)
		t.Cleanup(srv.Close)

		_, err := newITunesOnly(t, srv).fetchiTunes(ctx, providerArtist, providerAlbum)
		if err == nil {
			t.Fatal("fetchiTunes = nil, want error")
		}
		if apperrors.Is(err, apperrors.ErrNotFound) {
			t.Error("fetchiTunes = NOT_FOUND, want rate-limit error")
		}
	})
}

func TestFetchWikimedia(t *testing.T) {
	ctx := context.Background()
	newWikiOnly := func(t *testing.T, srv *httptest.Server) *Fetcher {
		t.Helper()
		f, err := New(Config{UserAgent: "coda-test", CommonsBaseURL: srv.URL})
		if err != nil {
			t.Fatal(err)
		}
		return f
	}

	t.Run("title match prefers thumbnail", func(t *testing.T) {
		var srv *httptest.Server
		mux := http.NewServeMux()
		mux.HandleFunc("/w/api.php", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"query":{"pages":{
				"1":{"title":"File:%s live 2020.jpg","imageinfo":[
					{"url":"%s/img/orig.jpg","thumburl":"%s/img/thumb.jpg","mime":"image/jpeg","size":1234}]}}}}`,
				providerArtist, srv.URL, srv.URL)
		})
		mux.HandleFunc("/img/", servePNG)
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		})
		srv = httptest.NewServer(mux)
		t.Cleanup(srv.Close)

		res, err := newWikiOnly(t, srv).fetchWikimedia(ctx, providerArtist)
		if err != nil {
			t.Fatalf("fetchWikimedia = %v", err)
		}
		if res.Source != SourceWikimedia {
			t.Errorf("source = %q, want wikimedia", res.Source)
		}
		if want := srv.URL + "/img/thumb.jpg"; res.SourceURL != want {
			t.Errorf("sourceURL = %q, want %q (thumbnail first)", res.SourceURL, want)
		}
	})

	t.Run("non-image mime skipped", func(t *testing.T) {
		var srv *httptest.Server
		mux := http.NewServeMux()
		mux.HandleFunc("/w/api.php", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"query":{"pages":{
				"1":{"title":"File:%s logo.svg","imageinfo":[
					{"url":"%s/img/logo.svg","mime":"image/svg+xml","size":10}]}}}}`,
				providerArtist, srv.URL)
		})
		srv = httptest.NewServer(mux)
		t.Cleanup(srv.Close)

		if _, err := newWikiOnly(t, srv).fetchWikimedia(ctx, providerArtist); !apperrors.Is(err, apperrors.ErrNotFound) {
			t.Errorf("fetchWikimedia = %v, want NOT_FOUND", err)
		}
	})

	t.Run("unrelated titles rejected", func(t *testing.T) {
		var srv *httptest.Server
		mux := http.NewServeMux()
		mux.HandleFunc("/w/api.php", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"query":{"pages":{
				"1":{"title":"File:Some Venue crowd.jpg","imageinfo":[
					{"url":"%s/img/crowd.jpg","mime":"image/jpeg","size":10}]}}}}`, srv.URL)
		})
		srv = httptest.NewServer(mux)
		t.Cleanup(srv.Close)

		if _, err := newWikiOnly(t, srv).fetchWikimedia(ctx, providerArtist); !apperrors.Is(err, apperrors.ErrNotFound) {
			t.Errorf("fetchWikimedia = %v, want NOT_FOUND", err)
		}
	})

	t.Run("page order is deterministic", func(t *testing.T) {
		var srv *httptest.Server
		mux := http.NewServeMux()
		mux.HandleFunc("/w/api.php", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			// Both pages match; the lexicographically smaller title must
			// win on every iteration despite the map ordering.
			_, _ = fmt.Fprintf(w, `{"query":{"pages":{
				"9":{"title":"File:Zebra %s portrait.jpg","imageinfo":[
					{"url":"%s/img/zebra.jpg","mime":"image/jpeg","size":10}]},
				"3":{"title":"File:Apple %s portrait.jpg","imageinfo":[
					{"url":"%s/img/apple.jpg","mime":"image/jpeg","size":10}]}}}}`,
				providerArtist, srv.URL, providerArtist, srv.URL)
		})
		mux.HandleFunc("/img/", servePNG)
		srv = httptest.NewServer(mux)
		t.Cleanup(srv.Close)

		f := newWikiOnly(t, srv)
		for i := range 20 {
			res, err := f.fetchWikimedia(ctx, providerArtist)
			if err != nil {
				t.Fatalf("iter %d: fetchWikimedia = %v", i, err)
			}
			if want := srv.URL + "/img/apple.jpg"; res.SourceURL != want {
				t.Fatalf("iter %d: sourceURL = %q, want %q", i, res.SourceURL, want)
			}
		}
	})
}

func TestFetchChain(t *testing.T) {
	ctx := context.Background()

	t.Run("caa short-circuits", func(t *testing.T) {
		var dzHits, itHits atomic.Int64
		mux := http.NewServeMux()
		mux.HandleFunc("/release/", func(w http.ResponseWriter, r *http.Request) {
			servePNG(w, r)
		})
		mux.HandleFunc("/search/album", func(w http.ResponseWriter, r *http.Request) {
			dzHits.Add(1)
			w.WriteHeader(http.StatusInternalServerError)
		})
		mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
			itHits.Add(1)
			w.WriteHeader(http.StatusInternalServerError)
		})
		srv := httptest.NewServer(mux)
		t.Cleanup(srv.Close)

		res, err := chainFetcher(t, srv.URL).Fetch(ctx, providerMBID, providerArtist, providerAlbum)
		if err != nil {
			t.Fatalf("Fetch = %v", err)
		}
		if res.Source != SourceCoverArtArchive {
			t.Errorf("source = %q, want coverartarchive", res.Source)
		}
		if dzHits.Load() != 0 || itHits.Load() != 0 {
			t.Errorf("fallbacks hit (deezer=%d itunes=%d), want none", dzHits.Load(), itHits.Load())
		}
	})

	t.Run("falls through to itunes", func(t *testing.T) {
		var srv *httptest.Server
		mux := http.NewServeMux()
		mux.HandleFunc("/release/", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		})
		mux.HandleFunc("/search/album", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"data":[]}`)
		})
		mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"resultCount":1,"results":[{"artistName":"%s",
				"collectionName":"%s","artworkUrl600":"%s/art/a.jpg"}]}`,
				providerArtist, providerAlbum, srv.URL)
		})
		mux.HandleFunc("/art/", servePNG)
		srv = httptest.NewServer(mux)
		t.Cleanup(srv.Close)

		res, err := chainFetcher(t, srv.URL).Fetch(ctx, providerMBID, providerArtist, providerAlbum)
		if err != nil {
			t.Fatalf("Fetch = %v", err)
		}
		if res.Source != SourceiTunes {
			t.Errorf("source = %q, want itunes", res.Source)
		}
	})

	t.Run("all miss is not found", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		})
		srv := httptest.NewServer(mux)
		t.Cleanup(srv.Close)

		if _, err := chainFetcher(t, srv.URL).Fetch(ctx, providerMBID, providerArtist, providerAlbum); !apperrors.Is(err, apperrors.ErrNotFound) {
			t.Errorf("Fetch = %v, want NOT_FOUND", err)
		}
	})

	t.Run("transient failure surfaces, not a miss", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/release/", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
		})
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		})
		srv := httptest.NewServer(mux)
		t.Cleanup(srv.Close)

		_, err := chainFetcher(t, srv.URL).Fetch(ctx, providerMBID, providerArtist, providerAlbum)
		if err == nil {
			t.Fatal("Fetch = nil, want upstream error")
		}
		if apperrors.Is(err, apperrors.ErrNotFound) {
			t.Error("Fetch = NOT_FOUND, want upstream error (callers must retry, not cache a miss)")
		}
		if !apperrors.Is(err, apperrors.ErrInternal) {
			t.Errorf("Fetch = %v, want INTERNAL_ERROR", err)
		}
	})

	t.Run("oversized caa falls through to deezer", func(t *testing.T) {
		var srv *httptest.Server
		mux := http.NewServeMux()
		mux.HandleFunc("/release/", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "image/png")
			// Well over the 1KB test cap; the chain must try Deezer next
			// instead of failing the fetch.
			_, _ = w.Write(make([]byte, 2048))
		})
		mux.HandleFunc("/search/album", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"data":[{"title":"%s","artist":{"name":"%s"},
				"cover_xl":"%s/img/xl.png"}]}`, providerAlbum, providerArtist, srv.URL)
		})
		mux.HandleFunc("/img/", servePNG)
		srv = httptest.NewServer(mux)
		t.Cleanup(srv.Close)

		f, err := New(Config{
			UserAgent: "coda-test", CAABaseURL: srv.URL, DeezerBaseURL: srv.URL,
			ITunesBaseURL: srv.URL, MaxBytes: 1024,
		})
		if err != nil {
			t.Fatal(err)
		}
		res, err := f.Fetch(ctx, providerMBID, providerArtist, providerAlbum)
		if err != nil {
			t.Fatalf("Fetch = %v", err)
		}
		if res.Source != SourceDeezer {
			t.Errorf("source = %q, want deezer (oversized CAA skipped)", res.Source)
		}
	})

	t.Run("invalid mbid", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/", servePNG)
		srv := httptest.NewServer(mux)
		t.Cleanup(srv.Close)

		if _, err := chainFetcher(t, srv.URL).Fetch(ctx, "not-a-uuid", providerArtist, providerAlbum); !apperrors.Is(err, apperrors.ErrInvalidID) {
			t.Errorf("Fetch = %v, want INVALID_ID", err)
		}
	})
}

func TestFetchArtistChain(t *testing.T) {
	ctx := context.Background()

	t.Run("deezer first, wikimedia fallback", func(t *testing.T) {
		var srv *httptest.Server
		mux := http.NewServeMux()
		mux.HandleFunc("/search/artist", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		})
		mux.HandleFunc("/w/api.php", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"query":{"pages":{
				"1":{"title":"File:%s 2020.jpg","imageinfo":[
					{"url":"%s/img/photo.jpg","mime":"image/jpeg","size":10}]}}}}`,
				providerArtist, srv.URL)
		})
		mux.HandleFunc("/img/", servePNG)
		srv = httptest.NewServer(mux)
		t.Cleanup(srv.Close)

		res, err := chainFetcher(t, srv.URL).FetchArtist(ctx, providerMBID, providerArtist)
		if err != nil {
			t.Fatalf("FetchArtist = %v", err)
		}
		if res.Source != SourceWikimedia {
			t.Errorf("source = %q, want wikimedia", res.Source)
		}
	})

	t.Run("empty name is a miss without requests", func(t *testing.T) {
		var hits atomic.Int64
		mux := http.NewServeMux()
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			w.WriteHeader(http.StatusInternalServerError)
		})
		srv := httptest.NewServer(mux)
		t.Cleanup(srv.Close)

		if _, err := chainFetcher(t, srv.URL).FetchArtist(ctx, providerMBID, "  "); !apperrors.Is(err, apperrors.ErrNotFound) {
			t.Errorf("FetchArtist = %v, want NOT_FOUND", err)
		}
		if hits.Load() != 0 {
			t.Errorf("requests = %d, want 0", hits.Load())
		}
	})

	t.Run("invalid mbid", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/", servePNG)
		srv := httptest.NewServer(mux)
		t.Cleanup(srv.Close)

		if _, err := chainFetcher(t, srv.URL).FetchArtist(ctx, "nope", providerArtist); !apperrors.Is(err, apperrors.ErrInvalidID) {
			t.Errorf("FetchArtist = %v, want INVALID_ID", err)
		}
	})
}

func TestDownloadGuards(t *testing.T) {
	ctx := context.Background()

	t.Run("oversized image is too large", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/", servePNG)
		srv := httptest.NewServer(mux)
		t.Cleanup(srv.Close)

		f, err := New(Config{UserAgent: "coda-test", CAABaseURL: srv.URL, MaxBytes: 10})
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := f.download(ctx, srv.URL+"/big.png"); !stderrors.Is(err, errImageTooLarge) {
			t.Errorf("download = %v, want image-too-large", err)
		}
	})

	t.Run("empty body fails", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
		srv := httptest.NewServer(mux)
		t.Cleanup(srv.Close)

		f, err := New(Config{UserAgent: "coda-test", CAABaseURL: srv.URL})
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := f.download(ctx, srv.URL+"/empty.png"); err == nil {
			t.Error("download = nil, want empty-image error")
		} else if apperrors.Is(err, apperrors.ErrNotFound) {
			t.Errorf("download = NOT_FOUND, want content error")
		}
	})

	t.Run("non-image content fails", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprint(w, "<html><body>nope</body></html>")
		})
		srv := httptest.NewServer(mux)
		t.Cleanup(srv.Close)

		f, err := New(Config{UserAgent: "coda-test", CAABaseURL: srv.URL})
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := f.download(ctx, srv.URL+"/page"); err == nil {
			t.Error("download = nil, want content-type error")
		} else if apperrors.Is(err, apperrors.ErrNotFound) {
			t.Errorf("download = NOT_FOUND, want content error")
		}
	})
}
