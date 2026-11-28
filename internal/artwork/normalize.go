package artwork

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const (
	// MaxCoverDimension is the longest side of a cached cover image.
	// Larger images are downscaled to this size; smaller ones keep
	// their dimensions. 1200px is plenty for the web UI and embedded
	// front covers while keeping caches and audio files small.
	MaxCoverDimension = 1200
	// CoverJPEGQuality is the JPEG quality used when normalizing cached
	// cover art. 85 is visually transparent for photos at display size.
	CoverJPEGQuality = 85

	// maxDecodePixels bounds the decoded pixel count before a full
	// decode, so a crafted small file with huge dimensions cannot blow
	// up memory. 40MP covers anything a provider serves.
	maxDecodePixels = 40_000_000
	// maxDecodeSide bounds a single image side for the same reason.
	maxDecodeSide = 10_000
)

// Normalize decodes data (JPEG, PNG, or WebP), downscales the longest
// side to MaxCoverDimension, flattens transparency onto white, and
// re-encodes as JPEG. It returns the normalized bytes and their MIME
// type (always image/jpeg). Images that are already small JPEGs are
// returned unchanged so provider art is never recompressed twice —
// SaveCover calls this as a safety net after the fetcher already
// normalized, and uploads funnel through the same path.
//
// A corrupt, unsupported, or absurdly large image is a plain error:
// callers classify it (provider failure vs invalid upload input).
func Normalize(data []byte) ([]byte, string, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, "", fmt.Errorf("artwork: empty image")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, "", fmt.Errorf("artwork: decode image config: %w", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 ||
		cfg.Width > maxDecodeSide || cfg.Height > maxDecodeSide ||
		int64(cfg.Width)*int64(cfg.Height) > maxDecodePixels {
		return nil, "", fmt.Errorf("artwork: image dimensions %dx%d out of range",
			cfg.Width, cfg.Height)
	}
	// Already a small JPEG: keep the original bytes so a second pass
	// (fetcher normalized, SaveCover nets again) never recompresses.
	if format == "jpeg" && cfg.Width <= MaxCoverDimension && cfg.Height <= MaxCoverDimension {
		return data, "image/jpeg", nil
	}

	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", fmt.Errorf("artwork: decode image: %w", err)
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w != cfg.Width || h != cfg.Height {
		return nil, "", fmt.Errorf("artwork: image config mismatch")
	}

	// Flatten onto an opaque white canvas first: JPEG has no alpha,
	// and scaling a transparent source would smear undefined RGB from
	// fully transparent pixels into the visible edges.
	flat := image.NewRGBA(image.Rect(0, 0, w, h))
	white := image.NewUniform(image.White)
	draw.Draw(flat, flat.Bounds(), white, image.Point{}, draw.Src)
	draw.Draw(flat, flat.Bounds(), src, b.Min, draw.Over)

	scaled := flat
	if long := max(w, h); long > MaxCoverDimension {
		nw := max(w*MaxCoverDimension/long, 1)
		nh := max(h*MaxCoverDimension/long, 1)
		dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
		draw.CatmullRom.Scale(dst, dst.Bounds(), flat, flat.Bounds(), draw.Over, nil)
		scaled = dst
	}

	var out bytes.Buffer
	if err := jpeg.Encode(&out, scaled, &jpeg.Options{Quality: CoverJPEGQuality}); err != nil {
		return nil, "", fmt.Errorf("artwork: encode cover: %w", err)
	}
	return out.Bytes(), "image/jpeg", nil
}
