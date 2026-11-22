package artwork

import (
	"context"

	apperrors "github.com/ppablomunoz/coda/internal/errors"
)

// fetchCAA downloads the designated front image for a release. CAA answers
// 307 to the archive.org-hosted bytes (followed by the HTTP client) and 404
// when the release has no front art. Already-classified errors pass through
// untouched so their Code survives for errors.Is.
func (f *Fetcher) fetchCAA(ctx context.Context, releaseMBID string) (*Result, error) {
	url := f.caaBase + "/release/" + releaseMBID + "/front"
	body, mime, err := f.download(ctx, url)
	if err != nil {
		if apperrors.Is(err, apperrors.ErrNotFound) {
			return nil, apperrors.ErrNotFound
		}
		return nil, err
	}
	return &Result{
		Bytes:     body,
		MIME:      mime,
		Source:    SourceCoverArtArchive,
		SourceURL: url,
	}, nil
}
