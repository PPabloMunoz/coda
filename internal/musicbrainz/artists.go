package musicbrainz

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

type Artist struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	SortName       string `json:"sort-name"`
	Disambiguation string `json:"disambiguation"`
}

type ArtistSearchResult struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	SortName       string `json:"sort-name"`
	Disambiguation string `json:"disambiguation"`
	Type           string `json:"type"`
	Country        string `json:"country"`
	Score          int    `json:"score"`
}

// ArtistCredit keeps the credited name, join phrase and canonical artist
// separate; never flatten it to a single string.
type ArtistCredit struct {
	Name       string `json:"name"`
	JoinPhrase string `json:"joinphrase"`
	Artist     Artist `json:"artist"`
}

func (c *Client) GetArtistByID(ctx context.Context, id string) (*Artist, error) {
	var artist Artist
	if err := c.getJSON(ctx, "/artist/"+id, nil, &artist); err != nil {
		return nil, fmt.Errorf("musicbrainz: artist: %w", err)
	}

	return &artist, nil
}

func (c *Client) SearchArtists(
	ctx context.Context,
	query string,
	limit int,
) ([]ArtistSearchResult, error) {
	if limit < 1 {
		limit = 10
	}
	if limit > 25 {
		limit = 25
	}

	escaped := strings.ReplaceAll(
		strings.ReplaceAll(strings.TrimSpace(query), `\`, `\\`),
		`"`,
		`\"`,
	)

	q := url.Values{}
	q.Set("query", `artist:"`+escaped+`"`)
	q.Set("limit", strconv.Itoa(limit))

	var res struct {
		Artists []ArtistSearchResult `json:"artists"`
	}
	if err := c.getJSON(ctx, "/artist", q, &res); err != nil {
		return nil, fmt.Errorf("musicbrainz: search artists: %w", err)
	}
	if res.Artists == nil {
		return []ArtistSearchResult{}, nil
	}

	return res.Artists, nil
}
