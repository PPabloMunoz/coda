package artwork

import (
	"context"

	apperrors "github.com/ppablomunoz/coda/internal/errors"
)

// fetchCAA downloads the designated front image for a release, asking for
// the 1200px thumbnail first: it matches the cache target, so it usually
// needs no downscale and decodes to ~1.4MP instead of tens of megapixels.
// Thumbnail derivation can lag behind the upload, so a missing thumbnail
// falls back to the original. CAA answers 307 to the archive.org-hosted
// bytes (followed by the HTTP client) and 404 when the release has no
// front art. Already-classified errors pass through untouched so their
// Code survives for errors.Is.
func (f *Fetcher) fetchCAA(ctx context.Context, releaseMBID string) (*Result, error) {
	thumb := f.caaBase + "/release/" + releaseMBID + "/front-1200"
	if body, _, err := f.download(ctx, thumb); err == nil {
		return f.normalizeResult(SourceCoverArtArchive, thumb, body)
	} else if !apperrors.Is(err, apperrors.ErrNotFound) {
		return nil, err
	}
	orig := f.caaBase + "/release/" + releaseMBID + "/front"
	body, _, err := f.download(ctx, orig)
	if err != nil {
		if apperrors.Is(err, apperrors.ErrNotFound) {
			return nil, apperrors.ErrNotFound
		}
		return nil, err
	}
	return f.normalizeResult(SourceCoverArtArchive, orig, body)
}
