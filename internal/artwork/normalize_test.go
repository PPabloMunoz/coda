package artwork

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"
)

// encodeTestPNG builds a w×h opaque PNG for normalize inputs.
func encodeTestPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode = %v", err)
	}
	return buf.Bytes()
}

func decodeDims(t *testing.T, data []byte) (format string, w, h int) {
	t.Helper()
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("DecodeConfig = %v", err)
	}
	return format, cfg.Width, cfg.Height
}

func TestNormalizeDownscalesLargePNGToJPEG(t *testing.T) {
	out, mime, err := Normalize(encodeTestPNG(t, 2400, 1600))
	if err != nil {
		t.Fatalf("Normalize = %v", err)
	}
	if mime != "image/jpeg" {
		t.Errorf("mime = %q, want image/jpeg", mime)
	}
	format, w, h := decodeDims(t, out)
	if format != "jpeg" {
		t.Errorf("format = %q, want jpeg", format)
	}
	if w != 1200 || h != 800 {
		t.Errorf("dims = %dx%d, want 1200x800", w, h)
	}
}

func TestNormalizeKeepsSmallJPEGUntouched(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 600, 400))
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatalf("jpeg.Encode = %v", err)
	}
	in := buf.Bytes()
	out, mime, err := Normalize(in)
	if err != nil {
		t.Fatalf("Normalize = %v", err)
	}
	if mime != "image/jpeg" {
		t.Errorf("mime = %q, want image/jpeg", mime)
	}
	if !bytes.Equal(out, in) {
		t.Error("small JPEG was recompressed, want original bytes")
	}
}

func TestNormalizeConvertsSmallPNGToJPEG(t *testing.T) {
	in := encodeTestPNG(t, 200, 200)
	out, mime, err := Normalize(in)
	if err != nil {
		t.Fatalf("Normalize = %v", err)
	}
	if mime != "image/jpeg" {
		t.Errorf("mime = %q, want image/jpeg", mime)
	}
	format, w, h := decodeDims(t, out)
	if format != "jpeg" || w != 200 || h != 200 {
		t.Errorf("got %s %dx%d, want jpeg 200x200", format, w, h)
	}
}

func TestNormalizeFlattensTransparency(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 100, 50))
	// Half opaque red, half fully transparent (undefined RGB underneath).
	for y := range 50 {
		for x := range 100 {
			if x < 50 {
				img.Set(x, y, color.NRGBA{R: 255, A: 255})
			} else {
				img.Set(x, y, color.NRGBA{})
			}
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode = %v", err)
	}
	out, _, err := Normalize(buf.Bytes())
	if err != nil {
		t.Fatalf("Normalize = %v", err)
	}
	got, _, err := image.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("Decode = %v", err)
	}
	r, g, b, _ := got.At(75, 25).RGBA()
	// Transparent half must come out near-white, not near-black.
	if r < 0xC000 || g < 0xC000 || b < 0xC000 {
		t.Errorf("transparent pixel = #%04x%04x%04x, want near-white", r, g, b)
	}
}

// TestFetchCAANormalizesLargeJPEG covers the real-world path end to end:
// a provider-sized JPEG is downscaled to a 1200px JPEG before caching,
// while a small JPEG passes through byte-identical (no double recompress).
func TestFetchCAANormalizesLargeJPEG(t *testing.T) {
	encode := func(w, h int) []byte {
		img := image.NewRGBA(image.Rect(0, 0, w, h))
		for y := range h {
			for x := range w {
				img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 128, A: 255})
			}
		}
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 95}); err != nil {
			t.Fatalf("jpeg.Encode = %v", err)
		}
		return buf.Bytes()
	}
	large, small := encode(2400, 1600), encode(600, 400)
	largeMBID := "11111111-2222-4333-a444-000000000001"
	smallMBID := "11111111-2222-4333-a444-000000000002"

	mux := http.NewServeMux()
	mux.HandleFunc("/release/"+largeMBID+"/front", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(large)
	})
	mux.HandleFunc("/release/"+smallMBID+"/front", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(small)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	f, err := New(Config{UserAgent: "coda-test", CAABaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New = %v", err)
	}
	ctx := context.Background()

	res, err := f.fetchCAA(ctx, largeMBID)
	if err != nil {
		t.Fatalf("fetchCAA large = %v", err)
	}
	if res.MIME != "image/jpeg" {
		t.Errorf("MIME = %q, want image/jpeg", res.MIME)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(res.Bytes))
	if err != nil {
		t.Fatalf("DecodeConfig = %v", err)
	}
	if format != "jpeg" || cfg.Width != 1200 || cfg.Height != 800 {
		t.Errorf("fetched = %s %dx%d, want jpeg 1200x800", format, cfg.Width, cfg.Height)
	}
	if len(res.Bytes) >= len(large) {
		t.Errorf("normalized %d bytes not smaller than %d-byte original", len(res.Bytes), len(large))
	}

	res, err = f.fetchCAA(ctx, smallMBID)
	if err != nil {
		t.Fatalf("fetchCAA small = %v", err)
	}
	if !bytes.Equal(res.Bytes, small) {
		t.Error("small JPEG was recompressed, want original bytes")
	}
}

func TestNormalizeRejectsGarbage(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"empty", nil},
		{"blank", []byte("   ")},
		{"text", []byte("not an image")},
		{"html", []byte("<html><body>nope</body></html>")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := Normalize(tc.data); err == nil {
				t.Error("Normalize = nil, want error")
			}
		})
	}
}
