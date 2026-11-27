package tasks

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ppablomunoz/coda/internal/artwork"
	apperrors "github.com/ppablomunoz/coda/internal/errors"
)

// missTestServer counts every provider request. notFound answers 404 for all
// paths; fail answers 500. art answers a valid PNG for the CAA path so a
// refresh/fetch succeeds.
func missTestServer(t *testing.T, calls *atomic.Int64, mode string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		switch mode {
		case "fail":
			w.WriteHeader(http.StatusInternalServerError)
		case "art":
			// Only CAA serves art; searches miss so the chain reaches CAA
			// first (CAA is tried before the text searches).
			if len(r.URL.Path) >= len("/release/") && r.URL.Path[:len("/release/")] == "/release/" {
				w.Header().Set("Content-Type", "image/png")
				_, _ = w.Write(embedPNG)
				return
			}
			w.WriteHeader(http.StatusNotFound)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestCoverMissBackoff(t *testing.T) {
	cases := []struct {
		misses int
		want   time.Duration
	}{
		{1, 6 * time.Hour},
		{2, 12 * time.Hour},
		{3, 24 * time.Hour},
		{4, 48 * time.Hour},
		{5, 96 * time.Hour},
		{6, 168 * time.Hour},
		{7, 168 * time.Hour},
		{0, 6 * time.Hour},
	}
	for _, c := range cases {
		if got := coverMissBackoff(c.misses); got != c.want {
			t.Errorf("backoff(%d) = %s, want %s", c.misses, got, c.want)
		}
	}
}

func TestEnsureCoverNegativeCache(t *testing.T) {
	root := t.TempDir()
	r, st := newEmbedRunner(t, root)
	seedEmbedRelease(t, st)
	ctx := context.Background()

	var calls atomic.Int64
	srv := missTestServer(t, &calls, "notFound")
	art := newTestFetcher(t, srv.URL)

	if _, err := EnsureCover(ctx, st, art, r.coverDir, embedRelID, "Embed Artist", "Embed Release"); !apperrors.Is(err, apperrors.ErrNotFound) {
		t.Fatalf("first EnsureCover = %v, want NOT_FOUND", err)
	}
	first := calls.Load()
	if first == 0 {
		t.Fatal("first miss made no provider calls")
	}
	m, err := st.GetReleaseCoverMiss(ctx, embedRelID)
	if err != nil {
		t.Fatalf("GetReleaseCoverMiss = %v", err)
	}
	if m.Misses != 1 {
		t.Errorf("misses = %d, want 1", m.Misses)
	}

	// A known miss makes no provider calls until the TTL expires.
	if _, err := EnsureCover(ctx, st, art, r.coverDir, embedRelID, "Embed Artist", "Embed Release"); !apperrors.Is(err, apperrors.ErrNotFound) {
		t.Fatalf("second EnsureCover = %v, want NOT_FOUND", err)
	}
	if got := calls.Load(); got != first {
		t.Errorf("provider calls after cached miss = %d, want %d (no new calls)", got, first)
	}

	// Expire the miss: the next GET refetches and backs off.
	past := time.Now().UTC().Add(-time.Hour)
	if err := st.UpsertReleaseCoverMiss(ctx, embedRelID, 1, past, past); err != nil {
		t.Fatalf("expire miss = %v", err)
	}
	if _, err := EnsureCover(ctx, st, art, r.coverDir, embedRelID, "Embed Artist", "Embed Release"); !apperrors.Is(err, apperrors.ErrNotFound) {
		t.Fatalf("EnsureCover after expiry = %v, want NOT_FOUND", err)
	}
	if got := calls.Load(); got <= first {
		t.Errorf("provider calls after expiry = %d, want > %d", got, first)
	}
	m, err = st.GetReleaseCoverMiss(ctx, embedRelID)
	if err != nil {
		t.Fatalf("GetReleaseCoverMiss after refetch = %v", err)
	}
	if m.Misses != 2 {
		t.Errorf("misses after refetch = %d, want 2", m.Misses)
	}
}

func TestEnsureCoverTransientNotCached(t *testing.T) {
	root := t.TempDir()
	r, st := newEmbedRunner(t, root)
	seedEmbedRelease(t, st)
	ctx := context.Background()

	var calls atomic.Int64
	srv := missTestServer(t, &calls, "fail")
	art := newTestFetcher(t, srv.URL)

	if _, err := EnsureCover(ctx, st, art, r.coverDir, embedRelID, "Embed Artist", "Embed Release"); apperrors.Is(err, apperrors.ErrNotFound) {
		t.Fatalf("EnsureCover on outage = NOT_FOUND, want upstream error")
	}
	if _, err := st.GetReleaseCoverMiss(ctx, embedRelID); !apperrors.Is(err, apperrors.ErrNotFound) {
		t.Errorf("miss recorded on transient failure, want none")
	}
	first := calls.Load()
	if _, err := EnsureCover(ctx, st, art, r.coverDir, embedRelID, "Embed Artist", "Embed Release"); apperrors.Is(err, apperrors.ErrNotFound) {
		t.Fatalf("second EnsureCover on outage = NOT_FOUND, want upstream error")
	}
	if got := calls.Load(); got <= first {
		t.Errorf("provider calls after transient = %d, want > %d (no negative cache)", got, first)
	}
}

func TestEnsureCoverTombstoneBeatsMiss(t *testing.T) {
	root := t.TempDir()
	r, st := newEmbedRunner(t, root)
	seedEmbedRelease(t, st)
	ctx := context.Background()

	var calls atomic.Int64
	srv := missTestServer(t, &calls, "art")
	art := newTestFetcher(t, srv.URL)

	// Record an active miss, then delete: the tombstone wins and no
	// provider is called even though art is available upstream.
	now := time.Now().UTC()
	if err := st.UpsertReleaseCoverMiss(ctx, embedRelID, 1, now, now.Add(6*time.Hour)); err != nil {
		t.Fatalf("Upsert miss = %v", err)
	}
	if err := st.MarkCoverRemoved(ctx, embedRelID); err != nil {
		t.Fatalf("MarkCoverRemoved = %v", err)
	}
	if _, err := EnsureCover(ctx, st, art, r.coverDir, embedRelID, "Embed Artist", "Embed Release"); !apperrors.Is(err, apperrors.ErrNotFound) {
		t.Fatalf("EnsureCover removed = %v, want NOT_FOUND", err)
	}
	if got := calls.Load(); got != 0 {
		t.Errorf("provider calls for tombstoned cover = %d, want 0", got)
	}

	// An upload clears the tombstone and serves without fetching.
	if err := SaveCover(ctx, st, r.coverDir, embedRelID, &artwork.Result{
		Bytes: embedPNG, MIME: "image/png", Source: artwork.SourceUpload,
	}); err != nil {
		t.Fatalf("SaveCover = %v", err)
	}
	if _, err := EnsureCover(ctx, st, art, r.coverDir, embedRelID, "Embed Artist", "Embed Release"); err != nil {
		t.Fatalf("EnsureCover after upload = %v, want row", err)
	}
	if got := calls.Load(); got != 0 {
		t.Errorf("provider calls after upload = %d, want 0 (cache hit)", got)
	}
}

func TestRefreshCoverBypassesMiss(t *testing.T) {
	root := t.TempDir()
	r, st := newEmbedRunner(t, root)
	seedEmbedRelease(t, st)
	ctx := context.Background()

	var calls atomic.Int64
	srv := missTestServer(t, &calls, "art")
	art := newTestFetcher(t, srv.URL)

	// Record an active miss; an explicit refresh still fetches.
	now := time.Now().UTC()
	if err := st.UpsertReleaseCoverMiss(ctx, embedRelID, 1, now, now.Add(6*time.Hour)); err != nil {
		t.Fatalf("Upsert miss = %v", err)
	}
	row, err := RefreshCover(ctx, st, art, r.coverDir, embedRelID, "Embed Artist", "Embed Release")
	if err != nil {
		t.Fatalf("RefreshCover with active miss = %v, want success", err)
	}
	if row.Removed {
		t.Error("Removed = true after successful refresh, want false")
	}
	if _, err := st.GetReleaseCoverMiss(ctx, embedRelID); !apperrors.Is(err, apperrors.ErrNotFound) {
		t.Errorf("miss after successful refresh = %v, want cleared", err)
	}
}

func TestRefreshCoverNotFoundRecordsMiss(t *testing.T) {
	root := t.TempDir()
	r, st := newEmbedRunner(t, root)
	seedEmbedRelease(t, st)
	ctx := context.Background()

	var calls atomic.Int64
	srv := missTestServer(t, &calls, "notFound")
	art := newTestFetcher(t, srv.URL)

	if _, err := RefreshCover(ctx, st, art, r.coverDir, embedRelID, "Embed Artist", "Embed Release"); !apperrors.Is(err, apperrors.ErrNotFound) {
		t.Fatalf("RefreshCover no-art = %v, want NOT_FOUND", err)
	}
	if _, err := st.GetReleaseCoverMiss(ctx, embedRelID); err != nil {
		t.Errorf("miss after no-art refresh = %v, want recorded", err)
	}
	before := calls.Load()

	// A successful save first, then a no-art refresh: the old art survives
	// and no miss is recorded (the row answers the next GET).
	if err := SaveCover(ctx, st, r.coverDir, embedRelID, &artwork.Result{
		Bytes: embedPNG, MIME: "image/png", Source: artwork.SourceUpload,
	}); err != nil {
		t.Fatalf("SaveCover = %v", err)
	}
	if _, err := RefreshCover(ctx, st, art, r.coverDir, embedRelID, "Embed Artist", "Embed Release"); !apperrors.Is(err, apperrors.ErrNotFound) {
		t.Fatalf("RefreshCover no-art with cache = %v, want NOT_FOUND", err)
	}
	if _, err := st.GetReleaseCoverMiss(ctx, embedRelID); !apperrors.Is(err, apperrors.ErrNotFound) {
		t.Errorf("miss recorded over existing art, want none")
	}
	_ = before
}

func TestCoverJobsSkipsActiveMiss(t *testing.T) {
	root := t.TempDir()
	r, st := newEmbedRunner(t, root)
	seedEmbedRelease(t, st)
	ctx := context.Background()

	jobs, err := r.coverJobs(ctx, embedArtistID)
	if err != nil {
		t.Fatalf("coverJobs = %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(jobs))
	}
	now := time.Now().UTC()
	if err := st.UpsertReleaseCoverMiss(ctx, embedRelID, 1, now, now.Add(6*time.Hour)); err != nil {
		t.Fatalf("Upsert miss = %v", err)
	}
	jobs, err = r.coverJobs(ctx, embedArtistID)
	if err != nil {
		t.Fatalf("coverJobs after miss = %v", err)
	}
	if len(jobs) != 0 {
		t.Errorf("jobs after active miss = %d, want 0", len(jobs))
	}
}

func TestEnsureArtistCoverNegativeCache(t *testing.T) {
	root := t.TempDir()
	r, st := newEmbedRunner(t, root)
	seedEmbedRelease(t, st)
	ctx := context.Background()

	var calls atomic.Int64
	srv := missTestServer(t, &calls, "notFound")
	artFetcher := newTestFetcher(t, srv.URL)

	if _, err := EnsureArtistCover(ctx, st, artFetcher, r.coverDir, embedArtistID, "Embed Artist"); !apperrors.Is(err, apperrors.ErrNotFound) {
		t.Fatalf("first EnsureArtistCover = %v, want NOT_FOUND", err)
	}
	first := calls.Load()
	if first == 0 {
		t.Fatal("first artist miss made no provider calls")
	}
	if _, err := EnsureArtistCover(ctx, st, artFetcher, r.coverDir, embedArtistID, "Embed Artist"); !apperrors.Is(err, apperrors.ErrNotFound) {
		t.Fatalf("second EnsureArtistCover = %v, want NOT_FOUND", err)
	}
	if got := calls.Load(); got != first {
		t.Errorf("artist provider calls after cached miss = %d, want %d", got, first)
	}

	if err := st.MarkArtistCoverRemoved(ctx, embedArtistID); err != nil {
		t.Fatalf("MarkArtistCoverRemoved = %v", err)
	}
	if _, err := EnsureArtistCover(ctx, st, artFetcher, r.coverDir, embedArtistID, "Embed Artist"); !apperrors.Is(err, apperrors.ErrNotFound) {
		t.Fatalf("EnsureArtistCover removed = %v, want NOT_FOUND", err)
	}
	if got := calls.Load(); got != first {
		t.Errorf("artist provider calls for tombstone = %d, want %d (no new calls)", got, first)
	}
}

func TestPrefetchArtistCoverSkipsActiveMiss(t *testing.T) {
	root := t.TempDir()
	r, st := newEmbedRunner(t, root)
	seedEmbedRelease(t, st)
	ctx := context.Background()

	var calls atomic.Int64
	srv := missTestServer(t, &calls, "notFound")
	r.art = newTestFetcher(t, srv.URL)

	now := time.Now().UTC()
	if err := st.UpsertArtistCoverMiss(ctx, embedArtistID, 1, now, now.Add(6*time.Hour)); err != nil {
		t.Fatalf("Upsert artist miss = %v", err)
	}
	r.prefetchArtistCover(ctx, embedArtistID, "Embed Artist")
	if got := calls.Load(); got != 0 {
		t.Errorf("prefetch provider calls with active miss = %d, want 0", got)
	}
}
