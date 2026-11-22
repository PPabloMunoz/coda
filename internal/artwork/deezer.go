package artwork

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	apperrors "github.com/ppablomunoz/coda/internal/errors"
)

// isDeezerPlaceholder reports whether a Deezer CDN URL is the blank
// placeholder served when an item has no picture: the hash segment is empty
// (…/images/artist//…) and the file carries the 000000-80-0-0 blank mark.
// Skipped candidates degrade to a miss so the next provider is tried.
func isDeezerPlaceholder(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	for _, seg := range strings.Split(strings.Trim(u.Path, "/"), "/") {
		if seg == "" {
			return true
		}
	}
	return strings.Contains(u.Path, "000000-80-0-0")
}

// firstUsable returns the first URL that is neither empty nor a Deezer blank
// placeholder, preferring earlier entries (largest renditions first).
func firstUsable(urls ...string) string {
	for _, u := range urls {
		if u != "" && !isDeezerPlaceholder(u) {
			return u
		}
	}
	return ""
}

// Deezer's public search API needs no key, which makes it a good fallback
// when CAA has no front art. Matches are text-based, so callers pass the
// release artist + title and the first hit with any cover image wins,
// preferring the largest rendition.

// deezerAlbumSearch is the subset of /search/album we read.
type deezerAlbumSearch struct {
	Data []deezerAlbum `json:"data"`
}
type deezerAlbum struct {
	Title       string       `json:"title"`
	Artist      deezerCredit `json:"artist"`
	CoverXL     string       `json:"cover_xl"`
	CoverBig    string       `json:"cover_big"`
	CoverMedium string       `json:"cover_medium"`
	Cover       string       `json:"cover"`
}

type deezerCredit struct {
	Name string `json:"name"`
}

func (f *Fetcher) fetchDeezer(ctx context.Context, artist, album string) (*Result, error) {
	artist = strings.TrimSpace(artist)
	album = strings.TrimSpace(album)
	if artist == "" && album == "" {
		return nil, apperrors.ErrNotFound
	}

	var parts []string
	if artist != "" {
		parts = append(parts, `artist:"`+artist+`"`)
	}
	if album != "" {
		parts = append(parts, `album:"`+album+`"`)
	}
	q := url.Values{}
	q.Set("q", strings.Join(parts, " "))
	searchURL := f.deezerBase + "/search/album?" + q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL, nil)
	if err != nil {
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("artwork: build deezer request: %w", err),
		)
	}
	req.Header.Set("User-Agent", f.ua)
	req.Header.Set("Accept", "application/json")

	resp, err := f.http.Do(req)
	if err != nil {
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("artwork: deezer request: %w", err),
		)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return nil, apperrors.ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("artwork: deezer status %d", resp.StatusCode),
		)
	}

	// Search payloads are small; still cap the read so a rogue response
	// can't blow up memory.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("artwork: read deezer response: %w", err),
		)
	}
	var search deezerAlbumSearch
	if err := json.Unmarshal(raw, &search); err != nil {
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("artwork: decode deezer response: %w", err),
		)
	}
	coverURL := ""
	wantTitle, wantArtist := normName(album), normName(artist)
	for i, a := range search.Data {
		if i >= 10 {
			break
		}
		// Text search is fuzzy: only accept the expected release, so a
		// same-title stranger never donates its art (which would then be
		// embedded into the user's files).
		if normName(a.Title) != wantTitle {
			continue
		}
		if wantArtist != "" && normName(a.Artist.Name) != wantArtist {
			continue
		}
		// Blank placeholders (no art on Deezer's side) are skipped so the
		// chain falls through to the next provider instead of caching a
		// gray square.
		coverURL = firstUsable(
			a.CoverXL, a.CoverBig, a.CoverMedium, a.Cover,
		)
		if coverURL != "" {
			break
		}
	}
	if coverURL == "" {
		return nil, apperrors.ErrNotFound
	}

	body, mime, err := f.download(ctx, coverURL)
	if err != nil {
		if apperrors.Is(err, apperrors.ErrNotFound) {
			return nil, apperrors.ErrNotFound
		}
		return nil, err
	}
	return &Result{
		Bytes:     body,
		MIME:      mime,
		Source:    SourceDeezer,
		SourceURL: coverURL,
	}, nil
}

// deezerArtistSearch is the subset of /search/artist we read.
type deezerArtistSearch struct {
	Data []deezerArtist `json:"data"`
}

type deezerArtist struct {
	Name          string `json:"name"`
	PictureXL     string `json:"picture_xl"`
	PictureBig    string `json:"picture_big"`
	PictureMedium string `json:"picture_medium"`
	Picture       string `json:"picture"`
}

// fetchDeezerArtist downloads the artist picture for a name. First hit with
// any picture wins, preferring the largest rendition. No key needed.
func (f *Fetcher) fetchDeezerArtist(ctx context.Context, name string) (*Result, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, apperrors.ErrNotFound
	}

	q := url.Values{}
	q.Set("q", `artist:"`+name+`"`)
	searchURL := f.deezerBase + "/search/artist?" + q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL, nil)
	if err != nil {
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("artwork: build deezer artist request: %w", err),
		)
	}
	req.Header.Set("User-Agent", f.ua)
	req.Header.Set("Accept", "application/json")

	resp, err := f.http.Do(req)
	if err != nil {
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("artwork: deezer artist request: %w", err),
		)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return nil, apperrors.ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("artwork: deezer artist status %d", resp.StatusCode),
		)
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("artwork: read deezer artist response: %w", err),
		)
	}
	var search deezerArtistSearch
	if err := json.Unmarshal(raw, &search); err != nil {
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("artwork: decode deezer artist response: %w", err),
		)
	}

	pictureURL := ""
	want := normName(name)
	for i, a := range search.Data {
		if i >= 10 {
			break
		}
		// Text search is fuzzy: only accept the expected artist, so a
		// same-name stranger never donates its photo.
		if normName(a.Name) != want {
			continue
		}
		// Blank placeholders (no photo on Deezer's side) are skipped so the
		// chain falls through to the next provider instead of caching a
		// gray square.
		pictureURL = firstUsable(
			a.PictureXL, a.PictureBig, a.PictureMedium, a.Picture,
		)
		if pictureURL != "" {
			break
		}
	}
	if pictureURL == "" {
		return nil, apperrors.ErrNotFound
	}

	body, mime, err := f.download(ctx, pictureURL)
	if err != nil {
		if apperrors.Is(err, apperrors.ErrNotFound) {
			return nil, apperrors.ErrNotFound
		}
		return nil, err
	}
	return &Result{
		Bytes:     body,
		MIME:      mime,
		Source:    SourceDeezer,
		SourceURL: pictureURL,
	}, nil
}
