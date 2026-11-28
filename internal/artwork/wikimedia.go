package artwork

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"

	apperrors "github.com/ppablomunoz/coda/internal/errors"
)

// Wikimedia Commons needs no key and hosts portrait photos for most notable
// artists. The File-namespace text search is fuzzier than Deezer's artist
// index, so Commons sits second: only Deezer misses get here. Page order is
// normalized (sorted by title) because the API returns a map.

// commonsSearch is the subset of the MediaWiki query API we read.
type commonsSearch struct {
	Query struct {
		Pages map[string]commonsPage `json:"pages"`
	} `json:"query"`
}

type commonsPage struct {
	Title     string         `json:"title"`
	ImageInfo []commonsImage `json:"imageinfo"`
}

type commonsImage struct {
	URL      string `json:"url"`
	ThumbURL string `json:"thumburl"`
	MIME     string `json:"mime"`
	Size     int64  `json:"size"`
}

// fetchWikimedia searches Commons File pages for name and downloads the first
// image whose title contains the artist, preferring the 1000px thumbnail.
func (f *Fetcher) fetchWikimedia(ctx context.Context, name string) (*Result, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, apperrors.ErrNotFound
	}

	q := url.Values{}
	q.Set("action", "query")
	q.Set("format", "json")
	q.Set("generator", "search")
	q.Set("gsrsearch", name)
	q.Set("gsrnamespace", "6") // File: namespace only
	q.Set("gsrlimit", "10")
	q.Set("prop", "imageinfo")
	q.Set("iiprop", "url|mime|size")
	q.Set("iiurlwidth", "1000")
	searchURL := f.commonsBase + "/w/api.php?" + q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL, nil)
	if err != nil {
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("artwork: build wikimedia request: %w", err),
		)
	}
	req.Header.Set("User-Agent", f.ua)
	req.Header.Set("Accept", "application/json")

	resp, err := f.http.Do(req)
	if err != nil {
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("artwork: wikimedia request: %w", err),
		)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return nil, apperrors.ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("artwork: wikimedia status %d", resp.StatusCode),
		)
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("artwork: read wikimedia response: %w", err),
		)
	}
	var search commonsSearch
	if err := json.Unmarshal(raw, &search); err != nil {
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("artwork: decode wikimedia response: %w", err),
		)
	}

	pages := make([]commonsPage, 0, len(search.Query.Pages))
	for _, p := range search.Query.Pages {
		pages = append(pages, p)
	}
	sort.Slice(pages, func(i, j int) bool { return pages[i].Title < pages[j].Title })

	// First usable image wins, preferring the 1000px thumbnail over the
	// original upload. Filenames can't equality-match, so accept the first
	// page whose title contains the artist name — a venue shot or stranger
	// never donates its photo. The size cap is enforced by download below.
	photoURL := ""
	want := normName(name)
	for _, p := range pages {
		if !strings.Contains(normName(p.Title), want) {
			continue
		}
		for _, img := range p.ImageInfo {
			switch img.MIME {
			case "image/jpeg", "image/png", "image/webp":
				if img.ThumbURL != "" {
					photoURL = img.ThumbURL
				} else {
					photoURL = img.URL
				}
			}
			if photoURL != "" {
				break
			}
		}
		if photoURL != "" {
			break
		}
	}
	if photoURL == "" {
		return nil, apperrors.ErrNotFound
	}

	body, _, err := f.download(ctx, photoURL)
	if err != nil {
		if apperrors.Is(err, apperrors.ErrNotFound) {
			return nil, apperrors.ErrNotFound
		}
		return nil, err
	}
	return f.normalizeResult(SourceWikimedia, photoURL, body)
}
