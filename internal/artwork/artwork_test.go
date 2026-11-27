package artwork

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var testPNG = mustDecodeB64("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==")

func mustDecodeB64(s string) []byte {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

const testMBID = "33333333-4444-4555-b666-777777777777"

func TestFetchConcurrencyBound(t *testing.T) {
	var current, maxSeen atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("/release/", func(w http.ResponseWriter, r *http.Request) {
		n := current.Add(1)
		for {
			m := maxSeen.Load()
			if n <= m || maxSeen.CompareAndSwap(m, n) {
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
		current.Add(-1)
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(testPNG)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	f, err := New(Config{
		UserAgent:   "coda-test",
		CAABaseURL:  srv.URL,
		Concurrency: 2,
		// Long enough that only the semaphore bounds the run.
		FetchTimeout: 10 * time.Second,
		Timeout:      5 * time.Second,
	})
	if err != nil {
		t.Fatalf("New = %v", err)
	}

	const callers = 6
	var wg sync.WaitGroup
	errs := make([]error, callers)
	for i := range callers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = f.Fetch(context.Background(), testMBID, "Artist", "Album")
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("Fetch %d = %v, want success", i, err)
		}
	}
	if got := maxSeen.Load(); got > 2 {
		t.Errorf("max concurrent provider requests = %d, want <= 2", got)
	}
}

func TestFetchDeadlineBoundsSlowProvider(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Every provider endpoint hangs; the per-cover deadline must cut
		// the chain off instead of waiting out each HTTP timeout. Stop
		// sleeping when the client gives up so srv.Close is fast.
		select {
		case <-time.After(2 * time.Second):
			w.WriteHeader(http.StatusNotFound)
		case <-r.Context().Done():
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	f, err := New(Config{
		UserAgent:     "coda-test",
		CAABaseURL:    srv.URL,
		DeezerBaseURL: srv.URL,
		ITunesBaseURL: srv.URL,
		Concurrency:   8,
		FetchTimeout:  200 * time.Millisecond,
		Timeout:       5 * time.Second,
	})
	if err != nil {
		t.Fatalf("New = %v", err)
	}

	start := time.Now()
	_, err = f.Fetch(context.Background(), testMBID, "Artist", "Album")
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("Fetch = nil, want deadline error")
	}
	if elapsed > 1500*time.Millisecond {
		t.Errorf("Fetch took %s, want well under the 2s provider hang", elapsed)
	}
}

func TestFetchArtistDeadlineBoundsSlowProvider(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(2 * time.Second):
			w.WriteHeader(http.StatusNotFound)
		case <-r.Context().Done():
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	f, err := New(Config{
		UserAgent:      "coda-test",
		DeezerBaseURL:  srv.URL,
		CommonsBaseURL: srv.URL,
		Concurrency:    8,
		FetchTimeout:   200 * time.Millisecond,
		Timeout:        5 * time.Second,
	})
	if err != nil {
		t.Fatalf("New = %v", err)
	}

	start := time.Now()
	_, err = f.FetchArtist(context.Background(), testMBID, "Artist")
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("FetchArtist = nil, want deadline error")
	}
	if elapsed > 1500*time.Millisecond {
		t.Errorf("FetchArtist took %s, want well under the 2s provider hang", elapsed)
	}
}
