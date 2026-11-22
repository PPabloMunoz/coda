package artwork

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	apperrors "github.com/ppablomunoz/coda/internal/errors"
	"github.com/ppablomunoz/coda/internal/utils"
)

const (
	// Sources mirror the store CoverSource* values so Fetcher results can be
	// recorded directly. 'upload' is reserved for manual uploads, which
	// bypass the fetcher entirely.
	SourceCoverArtArchive = "coverartarchive"
	SourceDeezer          = "deezer"
	SourceUpload          = "upload"

	DefaultCAABaseURL          = "https://coverartarchive.org"
	DefaultDeezerBaseURL       = "https://api.deezer.com"
	defaultTimeout             = 15 * time.Second
	defaultMaxBytes      int64 = 10 << 20 // 10MB cap so a rogue response can't blow up the cache
)

// Result is one fetched cover image.
type Result struct {
	Bytes []byte
	MIME  string // image/jpeg, image/png or image/webp
	// Source is one of the Source* constants; SourceURL is the URL the bytes
	// were downloaded from (CAA front URL or Deezer CDN URL, '' for uploads).
	Source    string
	SourceURL string
}

type Config struct {
	// UserAgent is required (app name/version + contact), same convention as
	// the MusicBrainz client.
	UserAgent     string
	CAABaseURL    string
	DeezerBaseURL string
	Timeout       time.Duration
	MaxBytes      int64
	Logger        *utils.Logger // optional; transient failures log at Warn
}

type Fetcher struct {
	caaBase    string
	deezerBase string
	http       *http.Client
	ua         string
	maxBytes   int64
	log        *utils.Logger
}

func New(cfg Config) (*Fetcher, error) {
	if strings.TrimSpace(cfg.UserAgent) == "" {
		return nil, fmt.Errorf("artwork: UserAgent is required (app name/version + contact)")
	}
	if cfg.CAABaseURL == "" {
		cfg.CAABaseURL = DefaultCAABaseURL
	}
	if cfg.DeezerBaseURL == "" {
		cfg.DeezerBaseURL = DefaultDeezerBaseURL
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultTimeout
	}
	if cfg.MaxBytes <= 0 {
		cfg.MaxBytes = defaultMaxBytes
	}
	return &Fetcher{
		caaBase:    strings.TrimRight(cfg.CAABaseURL, "/"),
		deezerBase: strings.TrimRight(cfg.DeezerBaseURL, "/"),
		http:       &http.Client{Timeout: cfg.Timeout},
		ua:         cfg.UserAgent,
		maxBytes:   cfg.MaxBytes,
		log:        cfg.Logger,
	}, nil
}

// Fetch returns the release cover, trying providers in order (CAA, then
// Deezer). It returns ErrNotFound only when no provider has art; transient
// upstream failures surface as errors so callers retry later instead of
// caching a miss. Artist/album feed the Deezer text search only.
func (f *Fetcher) Fetch(ctx context.Context, releaseMBID, artist, album string) (*Result, error) {
	if err := utils.ValidateMBID(releaseMBID); err != nil {
		return nil, apperrors.ErrInvalidID
	}

	var caaErr, deezerErr error
	if res, err := f.fetchCAA(ctx, releaseMBID); err == nil {
		return res, nil
	} else if apperrors.Is(err, apperrors.ErrNotFound) {
		f.debugf("artwork: no CAA art for release %s", releaseMBID)
	} else {
		caaErr = err
		f.warnf("artwork: CAA fetch for release %s failed: %v", releaseMBID, err)
	}

	if res, err := f.fetchDeezer(ctx, artist, album); err == nil {
		return res, nil
	} else if apperrors.Is(err, apperrors.ErrNotFound) {
		f.debugf("artwork: no Deezer art for release %s", releaseMBID)
	} else {
		deezerErr = err
		f.warnf("artwork: Deezer fetch for release %s failed: %v", releaseMBID, err)
	}

	// Prefer surfacing a real failure over a miss so callers don't treat an
	// outage as "no art exists".
	if caaErr != nil {
		return nil, caaErr
	}
	if deezerErr != nil {
		return nil, deezerErr
	}
	return nil, apperrors.ErrNotFound
}

// download GETs url, capping the body at maxBytes, and sniffs the MIME type
// from magic bytes so an HTML error page never passes as art. Non-200s map
// to ErrNotFound (404) or a wrapped internal error.
func (f *Fetcher) download(ctx context.Context, url string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("artwork: build request: %w", err),
		)
	}
	req.Header.Set("User-Agent", f.ua)
	req.Header.Set("Accept", "image/*")

	resp, err := f.http.Do(req)
	if err != nil {
		return nil, "", apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("artwork: request: %w", err),
		)
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
		// keep going
	case http.StatusNotFound:
		return nil, "", apperrors.ErrNotFound
	default:
		return nil, "", apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("artwork: status %d", resp.StatusCode),
		)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, f.maxBytes+1))
	if err != nil {
		return nil, "", apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("artwork: read body: %w", err),
		)
	}
	if int64(len(body)) > f.maxBytes {
		return nil, "", apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("artwork: image exceeds %d bytes", f.maxBytes),
		)
	}
	if len(body) == 0 {
		return nil, "", apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("artwork: empty image"),
		)
	}
	mime := http.DetectContentType(body)
	switch mime {
	case "image/jpeg", "image/png", "image/webp":
		return body, mime, nil
	default:
		return nil, "", apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("artwork: unsupported content %q", mime),
		)
	}
}

func (f *Fetcher) warnf(format string, args ...any) {
	if f.log != nil {
		f.log.Warnf(format, args...)
	}
}

func (f *Fetcher) debugf(format string, args ...any) {
	if f.log != nil {
		f.log.Debugf(format, args...)
	}
}
