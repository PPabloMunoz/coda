package tasks

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"

	"github.com/ppablomunoz/coda/internal/artwork"
	"github.com/ppablomunoz/coda/internal/config"
	apperrors "github.com/ppablomunoz/coda/internal/errors"
)

// biggerPNG is distinct upload bytes that still sniff as PNG (magic prefix
// intact), so a successful refresh is observable on disk and in the row.
var biggerPNG = append(append([]byte(nil), embedPNG...), 0x00)

func newTestFetcher(t *testing.T, baseURL string) *artwork.Fetcher {
	t.Helper()
	f, err := artwork.New(artwork.Config{
		UserAgent:      "coda-test",
		CAABaseURL:     baseURL,
		DeezerBaseURL:  baseURL,
		ITunesBaseURL:  baseURL,
		CommonsBaseURL: baseURL,
	})
	if err != nil {
		t.Fatalf("artwork New = %v", err)
	}
	return f
}

func TestRefreshCoverSuccessSwapsCache(t *testing.T) {
	root := t.TempDir()
	r, st := newEmbedRunner(t, root)
	seedEmbedRelease(t, st)
	ctx := context.Background()

	if err := SaveCover(ctx, st, r.coverDir, embedRelID, &artwork.Result{
		Bytes: embedPNG, MIME: "image/png", Source: artwork.SourceUpload,
	}); err != nil {
		t.Fatalf("SaveCover = %v", err)
	}
	before, err := st.GetCover(ctx, embedRelID)
	if err != nil {
		t.Fatalf("GetCover = %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/release/", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(biggerPNG)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	row, err := RefreshCover(ctx, st, newTestFetcher(t, srv.URL), r.coverDir,
		embedRelID, "Embed Artist", "Embed Release")
	if err != nil {
		t.Fatalf("RefreshCover = %v", err)
	}
	if row.Source == artwork.SourceUpload {
		t.Errorf("source = upload, want provider art after refresh")
	}
	if row.Removed {
		t.Error("Removed = true after successful refresh, want false")
	}
	if row.SizeBytes != int64(len(biggerPNG)) {
		t.Errorf("size = %d, want %d", row.SizeBytes, len(biggerPNG))
	}
	if row.FetchedAt == before.FetchedAt {
		t.Error("fetched_at unchanged after successful refresh")
	}
	path, err := config.CoverPath(r.coverDir, embedRelID, row.MIME)
	if err != nil {
		t.Fatalf("CoverPath = %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile = %v", err)
	}
	if !bytes.Equal(got, biggerPNG) {
		t.Error("cached bytes are not the refreshed art")
	}
}

func TestRefreshCoverFailureKeepsOldArt(t *testing.T) {
	root := t.TempDir()
	r, st := newEmbedRunner(t, root)
	seedEmbedRelease(t, st)
	ctx := context.Background()

	if err := SaveCover(ctx, st, r.coverDir, embedRelID, &artwork.Result{
		Bytes: embedPNG, MIME: "image/png", Source: artwork.SourceUpload,
	}); err != nil {
		t.Fatalf("SaveCover = %v", err)
	}
	before, err := st.GetCover(ctx, embedRelID)
	if err != nil {
		t.Fatalf("GetCover = %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	if _, err := RefreshCover(ctx, st, newTestFetcher(t, srv.URL), r.coverDir,
		embedRelID, "Embed Artist", "Embed Release"); err == nil {
		t.Fatal("RefreshCover = nil, want upstream error")
	} else if apperrors.Is(err, apperrors.ErrNotFound) {
		t.Fatalf("RefreshCover = NOT_FOUND, want upstream error (old art must survive either way)")
	}

	after, err := st.GetCover(ctx, embedRelID)
	if err != nil {
		t.Fatalf("GetCover after failed refresh = %v (old row lost)", err)
	}
	if after.Source != before.Source || after.SizeBytes != before.SizeBytes ||
		after.FetchedAt != before.FetchedAt || after.MIME != before.MIME {
		t.Errorf("row changed by failed refresh: before=%+v after=%+v", before, after)
	}
	path, err := config.CoverPath(r.coverDir, embedRelID, before.MIME)
	if err != nil {
		t.Fatalf("CoverPath = %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile after failed refresh = %v (old bytes lost)", err)
	}
	if !bytes.Equal(got, embedPNG) {
		t.Error("cached bytes changed by failed refresh")
	}
}

func TestRefreshCoverNotFoundKeepsOldArt(t *testing.T) {
	root := t.TempDir()
	r, st := newEmbedRunner(t, root)
	seedEmbedRelease(t, st)
	ctx := context.Background()

	if err := SaveCover(ctx, st, r.coverDir, embedRelID, &artwork.Result{
		Bytes: embedPNG, MIME: "image/png", Source: artwork.SourceUpload,
	}); err != nil {
		t.Fatalf("SaveCover = %v", err)
	}
	before, err := st.GetCover(ctx, embedRelID)
	if err != nil {
		t.Fatalf("GetCover = %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	if _, err := RefreshCover(ctx, st, newTestFetcher(t, srv.URL), r.coverDir,
		embedRelID, "Embed Artist", "Embed Release"); !apperrors.Is(err, apperrors.ErrNotFound) {
		t.Fatalf("RefreshCover = %v, want NOT_FOUND", err)
	}

	after, err := st.GetCover(ctx, embedRelID)
	if err != nil {
		t.Fatalf("GetCover after no-art refresh = %v (old row lost)", err)
	}
	if after.Source != before.Source || after.SizeBytes != before.SizeBytes ||
		after.FetchedAt != before.FetchedAt {
		t.Errorf("row changed by no-art refresh: before=%+v after=%+v", before, after)
	}
	path, err := config.CoverPath(r.coverDir, embedRelID, before.MIME)
	if err != nil {
		t.Fatalf("CoverPath = %v", err)
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, embedPNG) {
		t.Errorf("cached bytes changed by no-art refresh (err=%v)", err)
	}
}

func TestRefreshCoverClearsTombstone(t *testing.T) {
	root := t.TempDir()
	r, st := newEmbedRunner(t, root)
	seedEmbedRelease(t, st)
	ctx := context.Background()

	if err := SaveCover(ctx, st, r.coverDir, embedRelID, &artwork.Result{
		Bytes: embedPNG, MIME: "image/png", Source: artwork.SourceUpload,
	}); err != nil {
		t.Fatalf("SaveCover = %v", err)
	}
	if err := st.MarkCoverRemoved(ctx, embedRelID); err != nil {
		t.Fatalf("MarkCoverRemoved = %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/release/", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(biggerPNG)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	row, err := RefreshCover(ctx, st, newTestFetcher(t, srv.URL), r.coverDir,
		embedRelID, "Embed Artist", "Embed Release")
	if err != nil {
		t.Fatalf("RefreshCover = %v", err)
	}
	if row.Removed {
		t.Error("Removed = true after successful refresh, want tombstone cleared")
	}
}

func TestRefreshArtistCoverFailureKeepsOldPhoto(t *testing.T) {
	root := t.TempDir()
	r, st := newEmbedRunner(t, root)
	seedEmbedRelease(t, st)
	ctx := context.Background()

	if err := SaveArtistCover(ctx, st, r.coverDir, embedArtistID, &artwork.Result{
		Bytes: embedPNG, MIME: "image/png", Source: artwork.SourceUpload,
	}); err != nil {
		t.Fatalf("SaveArtistCover = %v", err)
	}
	before, err := st.GetArtistCover(ctx, embedArtistID)
	if err != nil {
		t.Fatalf("GetArtistCover = %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	if _, err := RefreshArtistCover(ctx, st, newTestFetcher(t, srv.URL), r.coverDir,
		embedArtistID, "Embed Artist"); err == nil {
		t.Fatal("RefreshArtistCover = nil, want upstream error")
	} else if apperrors.Is(err, apperrors.ErrNotFound) {
		t.Fatalf("RefreshArtistCover = NOT_FOUND, want upstream error")
	}

	after, err := st.GetArtistCover(ctx, embedArtistID)
	if err != nil {
		t.Fatalf("GetArtistCover after failed refresh = %v (old row lost)", err)
	}
	if after.Source != before.Source || after.SizeBytes != before.SizeBytes ||
		after.FetchedAt != before.FetchedAt || after.MIME != before.MIME {
		t.Errorf("row changed by failed refresh: before=%+v after=%+v", before, after)
	}
	path, err := config.ArtistCoverPath(r.coverDir, embedArtistID, before.MIME)
	if err != nil {
		t.Fatalf("ArtistCoverPath = %v", err)
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, embedPNG) {
		t.Errorf("cached photo changed by failed refresh (err=%v)", err)
	}
}

func TestRefreshArtistCoverSuccessSwapsPhoto(t *testing.T) {
	root := t.TempDir()
	r, st := newEmbedRunner(t, root)
	seedEmbedRelease(t, st)
	ctx := context.Background()

	if err := SaveArtistCover(ctx, st, r.coverDir, embedArtistID, &artwork.Result{
		Bytes: embedPNG, MIME: "image/png", Source: artwork.SourceUpload,
	}); err != nil {
		t.Fatalf("SaveArtistCover = %v", err)
	}

	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/search/artist", func(w http.ResponseWriter, req *http.Request) {
		pic := srv.URL + "/pic.png"
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"name":"Embed Artist",` +
			`"picture_xl":` + strconv.Quote(pic) + `,` +
			`"picture_big":` + strconv.Quote(pic) + `,` +
			`"picture_medium":` + strconv.Quote(pic) + `,` +
			`"picture":` + strconv.Quote(pic) + `}]}`))
	})
	mux.HandleFunc("/pic.png", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(biggerPNG)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	row, err := RefreshArtistCover(ctx, st, newTestFetcher(t, srv.URL), r.coverDir,
		embedArtistID, "Embed Artist")
	if err != nil {
		t.Fatalf("RefreshArtistCover = %v", err)
	}
	if row.Source == artwork.SourceUpload {
		t.Errorf("source = upload, want provider photo after refresh")
	}
	if row.Removed {
		t.Error("Removed = true after successful refresh, want false")
	}
	path, err := config.ArtistCoverPath(r.coverDir, embedArtistID, row.MIME)
	if err != nil {
		t.Fatalf("ArtistCoverPath = %v", err)
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, biggerPNG) {
		t.Errorf("cached photo is not the refreshed art (err=%v)", err)
	}
}

func TestRefreshArtistCoverNotFoundKeepsOldPhoto(t *testing.T) {
	root := t.TempDir()
	r, st := newEmbedRunner(t, root)
	seedEmbedRelease(t, st)
	ctx := context.Background()

	if err := SaveArtistCover(ctx, st, r.coverDir, embedArtistID, &artwork.Result{
		Bytes: embedPNG, MIME: "image/png", Source: artwork.SourceUpload,
	}); err != nil {
		t.Fatalf("SaveArtistCover = %v", err)
	}
	before, err := st.GetArtistCover(ctx, embedArtistID)
	if err != nil {
		t.Fatalf("GetArtistCover = %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/search/artist", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[]}`))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	if _, err := RefreshArtistCover(ctx, st, newTestFetcher(t, srv.URL), r.coverDir,
		embedArtistID, "Embed Artist"); !apperrors.Is(err, apperrors.ErrNotFound) {
		t.Fatalf("RefreshArtistCover = %v, want NOT_FOUND", err)
	}

	after, err := st.GetArtistCover(ctx, embedArtistID)
	if err != nil {
		t.Fatalf("GetArtistCover after no-photo refresh = %v (old row lost)", err)
	}
	if after.Source != before.Source || after.SizeBytes != before.SizeBytes ||
		after.FetchedAt != before.FetchedAt {
		t.Errorf("row changed by no-photo refresh: before=%+v after=%+v", before, after)
	}
}
