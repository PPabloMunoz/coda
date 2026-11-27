package artwork

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"

	apperrors "github.com/ppablomunoz/coda/internal/errors"
	"github.com/ppablomunoz/coda/internal/utils"
)

const (
	// Sources mirror the store CoverSource* values so Fetcher results can be
	// recorded directly. 'upload' is written by the upload handlers, which
	// bypass the fetcher entirely.
	SourceCoverArtArchive = "coverartarchive"
	SourceDeezer          = "deezer"
	SourceiTunes          = "itunes"
	SourceWikimedia       = "wikimedia"
	SourceUpload          = "upload"

	DefaultCAABaseURL     = "https://coverartarchive.org"
	DefaultDeezerBaseURL  = "https://api.deezer.com"
	DefaultiTunesBaseURL  = "https://itunes.apple.com"
	DefaultCommonsBaseURL = "https://commons.wikimedia.org"
	defaultTimeout        = 15 * time.Second
	// DefaultFetchConcurrency bounds concurrent provider chains across the
	// whole process. The background prefetch uses 4 workers, so 8 leaves
	// headroom for lazy GETs while keeping total outbound pressure bounded.
	DefaultFetchConcurrency = 8
	// DefaultFetchTimeout is the total deadline for one cover fetch
	// (all providers together), so one missing cover cannot hold a request
	// for the sum of every per-request HTTP timeout.
	DefaultFetchTimeout = 20 * time.Second
	// DefaultMaxBytes caps a single cached image at 15MB so a rogue
	// response can't blow up the cache. The upload handlers reuse the
	// same limit so cached bytes never exceed what the fetcher can write.
	DefaultMaxBytes int64 = 15 << 20
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
	UserAgent      string
	CAABaseURL     string
	DeezerBaseURL  string
	ITunesBaseURL  string
	CommonsBaseURL string
	Timeout        time.Duration
	MaxBytes       int64
	// Concurrency bounds concurrent provider chains (prefetch + lazy GETs
	// share one Fetcher, so one semaphore). <=0 defaults.
	Concurrency int
	// FetchTimeout is the total deadline for one Fetch/FetchArtist call,
	// covering the whole provider chain. <=0 defaults.
	FetchTimeout time.Duration
	Logger       *utils.Logger // optional; transient failures log at Warn
}

type Fetcher struct {
	caaBase      string
	deezerBase   string
	itunesBase   string
	commonsBase  string
	http         *http.Client
	ua           string
	maxBytes     int64
	fetchTimeout time.Duration
	// sem bounds concurrent provider chains process-wide; every Fetch and
	// FetchArtist holds one slot for its whole chain.
	sem chan struct{}
	log *utils.Logger
}

// errImageTooLarge is a control-flow signal, not an API error: download
// returns it when a provider's image exceeds maxBytes, and Fetch treats it
// like a miss so the next provider is tried. It never escapes Fetch.
var errImageTooLarge = errors.New("artwork: image too large")

// normName lowercases and strips diacritics so "Sofía Cámara" matches
// "Sofia Camara". Same recipe as store.normalizeSearch: text-search
// providers must verify candidate names or a same-name stranger wins.
func normName(s string) string {
	decomposed := norm.NFD.String(strings.TrimSpace(s))
	var b strings.Builder
	b.Grow(len(decomposed))
	for _, r := range decomposed {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		b.WriteRune(r)
	}
	return strings.ToLower(b.String())
}

// New validates config defaults and returns a cover/artist-photo Fetcher.
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
	if cfg.ITunesBaseURL == "" {
		cfg.ITunesBaseURL = DefaultiTunesBaseURL
	}
	if cfg.CommonsBaseURL == "" {
		cfg.CommonsBaseURL = DefaultCommonsBaseURL
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultTimeout
	}
	if cfg.MaxBytes <= 0 {
		cfg.MaxBytes = DefaultMaxBytes
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = DefaultFetchConcurrency
	}
	if cfg.FetchTimeout <= 0 {
		cfg.FetchTimeout = DefaultFetchTimeout
	}
	return &Fetcher{
		caaBase:      strings.TrimRight(cfg.CAABaseURL, "/"),
		deezerBase:   strings.TrimRight(cfg.DeezerBaseURL, "/"),
		itunesBase:   strings.TrimRight(cfg.ITunesBaseURL, "/"),
		commonsBase:  strings.TrimRight(cfg.CommonsBaseURL, "/"),
		http:         &http.Client{Timeout: cfg.Timeout},
		ua:           cfg.UserAgent,
		maxBytes:     cfg.MaxBytes,
		fetchTimeout: cfg.FetchTimeout,
		sem:          make(chan struct{}, cfg.Concurrency),
		log:          cfg.Logger,
	}, nil
}

// Debugf logs at debug level; tasks use it for cache hit/miss lines so
// cache behavior is visible with CODA_DEBUG=1 without a metrics subsystem.
func (f *Fetcher) Debugf(format string, args ...any) {
	f.debugf(format, args...)
}

// acquire holds one provider-chain slot until release is called. It honors
// ctx so a shutting-down request never waits forever for a slot.
func (f *Fetcher) acquire(ctx context.Context) (release func(), err error) {
	select {
	case f.sem <- struct{}{}:
		return func() { <-f.sem }, nil
	case <-ctx.Done():
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("artwork: wait for provider slot: %w", ctx.Err()),
		)
	}
}

// Fetch returns the release cover, trying providers in order (CAA, Deezer,
// then iTunes). It returns ErrNotFound only when no provider has usable art;
// transient upstream failures surface as errors so callers retry later
// instead of caching a miss. Artist/album feed the text-search fallbacks.
func (f *Fetcher) Fetch(ctx context.Context, releaseMBID, artist, album string) (*Result, error) {
	if err := utils.ValidateMBID(releaseMBID); err != nil {
		return nil, apperrors.ErrInvalidID
	}
	ctx, cancel := context.WithTimeout(ctx, f.fetchTimeout)
	defer cancel()
	release, err := f.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	f.debugf("artwork: fetch release %s", releaseMBID)

	var caaErr, deezerErr, itunesErr error
	if res, err := f.fetchCAA(ctx, releaseMBID); err == nil {
		f.debugf("artwork: fetched release %s from %s", releaseMBID, res.Source)
		return res, nil
	} else if apperrors.Is(err, apperrors.ErrNotFound) {
		f.debugf("artwork: no CAA art for release %s", releaseMBID)
	} else if errors.Is(err, errImageTooLarge) {
		f.debugf("artwork: CAA art for release %s exceeds size limit, trying fallback", releaseMBID)
	} else {
		caaErr = err
		f.warnf("artwork: CAA fetch for release %s failed: %v", releaseMBID, err)
	}

	if res, err := f.fetchDeezer(ctx, artist, album); err == nil {
		f.debugf("artwork: fetched release %s from %s", releaseMBID, res.Source)
		return res, nil
	} else if apperrors.Is(err, apperrors.ErrNotFound) {
		f.debugf("artwork: no Deezer art for release %s", releaseMBID)
	} else if errors.Is(err, errImageTooLarge) {
		f.debugf("artwork: Deezer art for release %s exceeds size limit, trying fallback", releaseMBID)
	} else {
		deezerErr = err
		f.warnf("artwork: Deezer fetch for release %s failed: %v", releaseMBID, err)
	}

	if res, err := f.fetchiTunes(ctx, artist, album); err == nil {
		f.debugf("artwork: fetched release %s from %s", releaseMBID, res.Source)
		return res, nil
	} else if apperrors.Is(err, apperrors.ErrNotFound) {
		f.debugf("artwork: no iTunes art for release %s", releaseMBID)
	} else if errors.Is(err, errImageTooLarge) {
		f.debugf("artwork: iTunes art for release %s exceeds size limit", releaseMBID)
	} else {
		itunesErr = err
		f.warnf("artwork: iTunes fetch for release %s failed: %v", releaseMBID, err)
	}

	// Prefer surfacing a real failure over a miss so callers don't treat an
	// outage as "no art exists".
	if caaErr != nil {
		return nil, caaErr
	}
	if deezerErr != nil {
		return nil, deezerErr
	}
	if itunesErr != nil {
		return nil, itunesErr
	}
	f.debugf("artwork: no art for release %s", releaseMBID)
	return nil, apperrors.ErrNotFound
}

// FetchArtist returns an artist photo, trying Deezer then Wikimedia Commons.
// Artist art is display-only (never embedded into files). It returns
// ErrNotFound only when no provider has usable art; transient upstream
// failures surface as errors so callers retry later instead of caching a
// miss.
func (f *Fetcher) FetchArtist(ctx context.Context, artistMBID, artistName string) (*Result, error) {
	if err := utils.ValidateMBID(artistMBID); err != nil {
		return nil, apperrors.ErrInvalidID
	}
	ctx, cancel := context.WithTimeout(ctx, f.fetchTimeout)
	defer cancel()
	release, err := f.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	f.debugf("artwork: fetch artist %s", artistMBID)

	var deezerErr, wikiErr error
	if res, err := f.fetchDeezerArtist(ctx, artistName); err == nil {
		f.debugf("artwork: fetched artist %s from %s", artistMBID, res.Source)
		return res, nil
	} else if apperrors.Is(err, apperrors.ErrNotFound) {
		f.debugf("artwork: no Deezer photo for artist %s", artistMBID)
	} else if errors.Is(err, errImageTooLarge) {
		f.debugf("artwork: Deezer photo for artist %s exceeds size limit, trying fallback", artistMBID)
	} else {
		deezerErr = err
		f.warnf("artwork: Deezer photo fetch for artist %s failed: %v", artistMBID, err)
	}

	if res, err := f.fetchWikimedia(ctx, artistName); err == nil {
		f.debugf("artwork: fetched artist %s from %s", artistMBID, res.Source)
		return res, nil
	} else if apperrors.Is(err, apperrors.ErrNotFound) {
		f.debugf("artwork: no Wikimedia photo for artist %s", artistMBID)
	} else if errors.Is(err, errImageTooLarge) {
		f.debugf("artwork: Wikimedia photo for artist %s exceeds size limit", artistMBID)
	} else {
		wikiErr = err
		f.warnf("artwork: Wikimedia fetch for artist %s failed: %v", artistMBID, err)
	}

	if deezerErr != nil {
		return nil, deezerErr
	}
	if wikiErr != nil {
		return nil, wikiErr
	}
	f.debugf("artwork: no photo for artist %s", artistMBID)
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
		// Too big for the cache: signal the chain to try the next
		// provider instead of failing the fetch.
		return nil, "", errImageTooLarge
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
