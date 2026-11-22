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

// iTunes Search needs no key (~20 calls/min per IP) and returns album art
// with a resizable URL token. It sits last in the chain: only CAA+Deezer
// misses get here, so the rate budget is rarely touched. A 403 means the
// budget was exceeded and surfaces as an error so the caller retries later
// instead of caching a miss.

// itunesAlbumSearch is the subset of /search we read.
type itunesAlbumSearch struct {
	ResultCount int           `json:"resultCount"`
	Results     []itunesAlbum `json:"results"`
}

type itunesAlbum struct {
	ArtistName     string `json:"artistName"`
	CollectionName string `json:"collectionName"`
	ArtworkURL100  string `json:"artworkUrl100"`
	ArtworkURL600  string `json:"artworkUrl600"`
}

// fetchiTunes searches iTunes albums by artist/album and downloads the first
// exact match, upgrading artwork to 600px. No match is ErrNotFound.
func (f *Fetcher) fetchiTunes(ctx context.Context, artist, album string) (*Result, error) {
	artist = strings.TrimSpace(artist)
	album = strings.TrimSpace(album)
	if artist == "" && album == "" {
		return nil, apperrors.ErrNotFound
	}

	terms := []string{}
	if artist != "" {
		terms = append(terms, artist)
	}
	if album != "" {
		terms = append(terms, album)
	}
	q := url.Values{}
	q.Set("term", strings.Join(terms, " "))
	q.Set("entity", "album")
	q.Set("country", "US")
	q.Set("limit", "5")
	searchURL := f.itunesBase + "/search?" + q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL, nil)
	if err != nil {
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("artwork: build itunes request: %w", err),
		)
	}
	req.Header.Set("User-Agent", f.ua)
	req.Header.Set("Accept", "application/json")

	resp, err := f.http.Do(req)
	if err != nil {
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("artwork: itunes request: %w", err),
		)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return nil, apperrors.ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("artwork: itunes status %d", resp.StatusCode),
		)
	}

	// Search payloads are small; still cap the read so a rogue response
	// can't blow up memory.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("artwork: read itunes response: %w", err),
		)
	}
	var search itunesAlbumSearch
	if err := json.Unmarshal(raw, &search); err != nil {
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("artwork: decode itunes response: %w", err),
		)
	}

	// First usable hit wins: the expected release with artwork, preferring
	// the larger rendition. Text search is fuzzy, so verify names — a
	// same-title stranger must never donate art that gets embedded into the
	// user's files.
	coverURL := ""
	wantTitle, wantArtist := normName(album), normName(artist)
	for _, a := range search.Results {
		if normName(a.CollectionName) != wantTitle {
			continue
		}
		if wantArtist != "" && normName(a.ArtistName) != wantArtist {
			continue
		}
		switch {
		case a.ArtworkURL600 != "":
			coverURL = a.ArtworkURL600
		case a.ArtworkURL100 != "":
			coverURL = a.ArtworkURL100
		}
		if coverURL != "" {
			break
		}
	}
	if coverURL == "" {
		return nil, apperrors.ErrNotFound
	}
	// Artwork URLs embed their size (…/100x100bb.jpg); ask for 600px.
	// Already-600 URLs are untouched by the replacement.
	coverURL = strings.Replace(coverURL, "100x100", "600x600", 1)

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
		Source:    SourceiTunes,
		SourceURL: coverURL,
	}, nil
}
