package musicbrainz

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	apperrors "github.com/ppablomunoz/coda/internal/errors"
)

type ReleaseGroup struct {
	ID           string         `json:"id"`
	Title        string         `json:"title"`
	PrimaryType  string         `json:"primary-type"`
	ArtistCredit []ArtistCredit `json:"artist-credit"`
}

type Release struct {
	ID           string         `json:"id"`
	Title        string         `json:"title"`
	Status       string         `json:"status"`
	Date         string         `json:"date"`
	ArtistCredit []ArtistCredit `json:"artist-credit"`
	ReleaseGroup ReleaseGroup   `json:"release-group"`
	Media        []Medium       `json:"media"`
}

type Medium struct {
	Position int            `json:"position"`
	Format   string         `json:"format"`
	Tracks   []ReleaseTrack `json:"tracks"`
}

type ReleaseTrack struct {
	ID           string         `json:"id"`
	Position     int            `json:"position"`
	Number       string         `json:"number"`
	Title        string         `json:"title"`
	Length       *int           `json:"length"`
	ArtistCredit []ArtistCredit `json:"artist-credit"`
	Recording    RecordingRef   `json:"recording"`
}

type RecordingRef struct {
	ID           string         `json:"id"`
	Title        string         `json:"title"`
	Length       *int           `json:"length"`
	ArtistCredit []ArtistCredit `json:"artist-credit"`
}

// BrowseReleasesByArtist streams official albums/EPs/singles with tracks
// from MusicBrainz one page at a time, invoking fn per page so callers
// never retain the whole discography in memory. Query filters are
// lowercase (official); response Status is capitalized (Official), so
// compare case-insensitively. Pages with no kept releases are skipped.
// A fn error stops pagination and is returned unwrapped.
// Releases are capped at 500 tracks per page, so the offset advances by
// what was actually returned, not by limit.
func (c *Client) BrowseReleasesByArtist(ctx context.Context, id string, fn func([]Release) error) error {
	offset := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		q := url.Values{}
		q.Set("artist", id)
		q.Set("type", "album|ep|single")
		q.Set("status", "official")
		q.Set("inc", "release-groups+recordings+artist-credits+media")
		q.Set("limit", "100")
		q.Set("offset", fmt.Sprintf("%d", offset))
		var res struct {
			Count    int       `json:"release-count"`
			Releases []Release `json:"releases"`
		}
		if err := c.getJSON(ctx, "/release", q, &res); err != nil {
			return fmt.Errorf("musicbrainz: releases: %w", err)
		}
		filtered := make([]Release, 0, len(res.Releases))
		for _, r := range res.Releases {
			if r.Status != "" && !strings.EqualFold(r.Status, "official") {
				continue
			}
			// Release itself has no primary type; gate on its group.
			if r.ReleaseGroup.PrimaryType != "" {
				switch r.ReleaseGroup.PrimaryType {
				case "Album", "EP", "Single": // keep
				default:
					continue
				}
			}
			filtered = append(filtered, r)
		}
		if len(filtered) > 0 {
			if err := fn(filtered); err != nil {
				return err
			}
		}
		offset += len(res.Releases)
		if offset >= res.Count || len(res.Releases) == 0 {
			break
		}
	}
	return nil
}

// GetReleaseByID fetches a single release with its parent release-group and
// tracklist. It only accepts official releases and hard-fails when the
// release does not belong to the expected release group.
func (c *Client) GetReleaseByID(ctx context.Context, releaseGroupID, releaseID string) (*Release, error) {
	q := url.Values{}
	q.Set("inc", "release-groups+recordings+artist-credits+media")
	var r Release
	if err := c.getJSON(ctx, "/release/"+releaseID, q, &r); err != nil {
		return nil, fmt.Errorf("musicbrainz: release: %w", err)
	}
	if r.Status != "" && !strings.EqualFold(r.Status, "official") {
		return nil, apperrors.Wrap(
			apperrors.ErrInvalidInput,
			fmt.Errorf("musicbrainz: release %s is not official (status %q)", releaseID, r.Status),
		)
	}
	if r.ReleaseGroup.ID != releaseGroupID {
		return nil, apperrors.Wrap(
			apperrors.ErrInvalidInput,
			fmt.Errorf("musicbrainz: release %s belongs to release-group %s, not %s", releaseID, r.ReleaseGroup.ID, releaseGroupID),
		)
	}
	return &r, nil
}
