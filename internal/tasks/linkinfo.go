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
