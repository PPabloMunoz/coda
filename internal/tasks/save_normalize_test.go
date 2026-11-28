package tasks

import (
	"bytes"
	"context"
	"image"
	_ "image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/ppablomunoz/coda/internal/artwork"
	"github.com/ppablomunoz/coda/internal/config"
)

// bigTestPNG builds a 2400x1600 PNG: normalization must downscale it to a
// 1200px JPEG so the cache and embedded art stay small.
func bigTestPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2400, 1600))
	for y := range 1600 {
		for x := range 2400 {
			img.Set(x, y, image.White)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode = %v", err)
	}
	return buf.Bytes()
}

// TestSaveCoverNormalizesPNGToJPEG saves provider-sized PNG bytes and
// asserts the cache holds a downscaled JPEG: row MIME and size, the .jpg
// file path, and decoded dimensions.
func TestSaveCoverNormalizesPNGToJPEG(t *testing.T) {
	root := t.TempDir()
	r, st := newEmbedRunner(t, root)
	seedEmbedRelease(t, st)
	ctx := context.Background()

	if err := SaveCover(ctx, st, r.coverDir, embedRelID, &artwork.Result{
		Bytes: bigTestPNG(t), MIME: "image/png", Source: artwork.SourceCoverArtArchive,
	}); err != nil {
		t.Fatalf("SaveCover = %v", err)
	}
	row, err := st.GetCover(ctx, embedRelID)
	if err != nil {
		t.Fatalf("GetCover = %v", err)
	}
	if row.MIME != "image/jpeg" {
		t.Errorf("MIME = %q, want image/jpeg", row.MIME)
	}
	path, err := config.CoverPath(r.coverDir, embedRelID, row.MIME)
	if err != nil {
		t.Fatalf("CoverPath = %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile = %v", err)
	}
	if int64(len(got)) != row.SizeBytes {
		t.Errorf("size = %d, want %d (row)", len(got), row.SizeBytes)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(got))
	if err != nil {
		t.Fatalf("DecodeConfig = %v", err)
	}
	if format != "jpeg" || cfg.Width != 1200 || cfg.Height != 800 {
		t.Errorf("cached = %s %dx%d, want jpeg 1200x800", format, cfg.Width, cfg.Height)
	}
	if _, err := os.Stat(filepath.Join(r.coverDir, embedRelID+".png")); !os.IsNotExist(err) {
		t.Error("stale png sibling survives normalization")
	}
}

// TestSaveArtistCoverNormalizesPNGToJPEG is the artist-photo half of the
// same guarantee: display-only photos are cached small too.
func TestSaveArtistCoverNormalizesPNGToJPEG(t *testing.T) {
	root := t.TempDir()
	r, st := newEmbedRunner(t, root)
	seedEmbedRelease(t, st)
	ctx := context.Background()

	if err := SaveArtistCover(ctx, st, r.coverDir, embedArtistID, &artwork.Result{
		Bytes: bigTestPNG(t), MIME: "image/png", Source: artwork.SourceWikimedia,
	}); err != nil {
		t.Fatalf("SaveArtistCover = %v", err)
	}
	row, err := st.GetArtistCover(ctx, embedArtistID)
	if err != nil {
		t.Fatalf("GetArtistCover = %v", err)
	}
	if row.MIME != "image/jpeg" {
		t.Errorf("MIME = %q, want image/jpeg", row.MIME)
	}
	path, err := config.ArtistCoverPath(r.coverDir, embedArtistID, row.MIME)
	if err != nil {
		t.Fatalf("ArtistCoverPath = %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile = %v", err)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(got))
	if err != nil {
		t.Fatalf("DecodeConfig = %v", err)
	}
	if format != "jpeg" || cfg.Width != 1200 || cfg.Height != 800 {
		t.Errorf("cached = %s %dx%d, want jpeg 1200x800", format, cfg.Width, cfg.Height)
	}
}

// TestEnsureCoverFetchedFlag reports whether the call fetched fresh bytes:
// callers queue the background re-embed only on a fresh fetch.
func TestEnsureCoverFetchedFlag(t *testing.T) {
	root := t.TempDir()
	r, st := newEmbedRunner(t, root)
	seedEmbedRelease(t, st)
	ctx := context.Background()

	var calls atomic.Int64
	srv := missTestServer(t, &calls, "art")
	art := newTestFetcher(t, srv.URL)

	row, fetched, err := EnsureCover(ctx, st, art, r.coverDir, embedRelID, "Embed Artist", "Embed Release")
	if err != nil {
		t.Fatalf("EnsureCover = %v", err)
	}
	if !fetched {
		t.Error("fetched = false on provider fetch, want true")
	}
	if row.MIME != "image/jpeg" {
		t.Errorf("MIME = %q, want image/jpeg (provider PNG normalized)", row.MIME)
	}

	row2, fetched2, err := EnsureCover(ctx, st, art, r.coverDir, embedRelID, "Embed Artist", "Embed Release")
	if err != nil {
		t.Fatalf("second EnsureCover = %v", err)
	}
	if fetched2 {
		t.Error("fetched = true on cache hit, want false")
	}
	if row2.FetchedAt != row.FetchedAt {
		t.Error("cache hit returned a different row")
	}
}
