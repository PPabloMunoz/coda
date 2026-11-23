package tasks

import (
	"strings"

	"github.com/ppablomunoz/coda/internal/store"
	"github.com/ppablomunoz/coda/internal/tags"
)

// LinkInfoFromStore builds the tag write model from a release and track.
// Single owner for store→tags link mapping (server reuses this instead of
// reimplementing credit fallbacks in the HTTP layer).
func LinkInfoFromStore(rel *store.FullReleaseParams, track *store.TrackParams) *tags.LinkInfo {
	trackDisplay, trackNames, trackIDs := creditDisplay(track.Credit)
	if len(trackNames) == 0 {
		// Fallback so ARTIST is never blank for Navidrome grouping.
		trackDisplay, trackNames, trackIDs = creditDisplay(rel.Credit)
	}

	albumDisplay, albumNames, albumIDs := creditDisplay(rel.Credit)
	if len(albumNames) == 0 {
		albumDisplay, albumNames, albumIDs = creditDisplay(rel.Group.Credit)
	}

	return &tags.LinkInfo{
		Title:              track.Title,
		Album:              rel.Title,
		ArtistDisplay:      trackDisplay,
		Artists:            trackNames,
		MBArtistIDs:        trackIDs,
		AlbumArtistDisplay: albumDisplay,
		AlbumArtists:       albumNames,
		MBAlbumArtistIDs:   albumIDs,
		TrackNumber:        track.Position,
		DiscNumber:         max(track.Disc, 1),
		DiscTotal:          maxDiscNumber(rel),
		Date:               deref(rel.Date),
		MBTrackID:          track.RecordingID,
		MBReleaseTrackID:   track.ID,
		MBAlbumID:          rel.ID,
		MBReleaseGroupID:   rel.Group.ID,
		ReleaseType:        rel.Group.Type,
		ReleaseStatus:      deref(rel.Status),
	}
}

// CreditDisplayName renders the joined credit display names ("A & B") for
// text-search fallbacks and cover attribution. Empty when there is no
// usable credit.
func CreditDisplayName(entries []store.CreditEntry) string {
	display, _, _ := creditDisplay(entries)
	return strings.TrimSpace(display)
}

// creditDisplay renders joined display names plus per-artist names and MBIDs.
func creditDisplay(entries []store.CreditEntry) (string, []string, []string) {
	var display strings.Builder
	names := make([]string, 0, len(entries))
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		name := e.CreditedName
		if strings.TrimSpace(name) == "" {
			name = e.Name
		}
		names = append(names, name)
		ids = append(ids, e.ArtistID)
		display.WriteString(name)
		display.WriteString(e.JoinPhrase)
	}
	return display.String(), names, ids
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// maxDiscNumber reports the highest disc number across the release's tracks,
// clamped to at least 1. A release with no track rows (or all disc <= 1) is
// treated as single-disc so filenames omit the disc prefix.
func maxDiscNumber(rel *store.FullReleaseParams) int {
	maxDisc := 1
	if rel != nil {
		for _, t := range rel.Tracks {
			if t.Disc > maxDisc {
				maxDisc = t.Disc
			}
		}
	}
	return maxDisc
}
