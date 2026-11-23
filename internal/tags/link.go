package tags

import (
	"fmt"
	"strconv"
	"strings"

	"go.senan.xyz/taglib"
)

type LinkInfo struct {
	Title, Album, ArtistDisplay, AlbumArtistDisplay, Date string
	Artists, AlbumArtists, MBArtistIDs, MBAlbumArtistIDs  []string
	TrackNumber, DiscNumber                               int
	// DiscTotal is the highest disc number on the release (1 for single-disc).
	// It drives filename formatting only; tags still write DiscNumber.
	DiscTotal                                                int
	MBTrackID, MBReleaseTrackID, MBAlbumID, MBReleaseGroupID string
	ReleaseType, ReleaseStatus                               string
}

// LinkKeys are the only keys link writes, and the only ones unlink clears.
func LinkKeys() []string {
	return []string{
		taglib.Title, taglib.Album, taglib.Artist, taglib.Artists,
		taglib.AlbumArtist, albumArtistsKey,
		taglib.TrackNumber, taglib.DiscNumber, taglib.Date,
		taglib.MusicBrainzTrackID, taglib.MusicBrainzReleaseTrackID,
		taglib.MusicBrainzAlbumID, taglib.MusicBrainzReleaseGroupID,
		taglib.MusicBrainzArtistID, taglib.MusicBrainzAlbumArtistID,
		taglib.ReleaseType, taglib.ReleaseStatus,
	}
}

// SnapshotFromLink builds a write-ready Snapshot from link info for tags.ToMap.
func SnapshotFromLink(info *LinkInfo) *Snapshot {
	s := &Snapshot{
		Title:            info.Title,
		Album:            info.Album,
		ArtistRaw:        info.ArtistDisplay,
		Artists:          info.Artists,
		AlbumArtist:      info.AlbumArtistDisplay,
		AlbumArtists:     info.AlbumArtists,
		TrackNumber:      info.TrackNumber,
		DiscNumber:       info.DiscNumber,
		Date:             info.Date,
		MBTrackID:        info.MBTrackID,
		MBReleaseTrackID: info.MBReleaseTrackID,
		MBAlbumID:        info.MBAlbumID,
		MBReleaseGroupID: info.MBReleaseGroupID,
		MBArtistIDs:      info.MBArtistIDs,
		MBAlbumArtistIDs: info.MBAlbumArtistIDs,
		ReleaseType:      info.ReleaseType,
		ReleaseStatus:    info.ReleaseStatus,
	}
	return s
}

// ToMap converts s to a taglib map with only LinkKeys, skipping blanks and zero numbers.
func ToMap(s *Snapshot) map[string][]string {
	out := map[string][]string{}
	put := func(k, v string) {
		if strings.TrimSpace(v) != "" {
			out[k] = []string{v}
		}
	}
	put(taglib.Title, s.Title)
	put(taglib.Album, s.Album)
	put(taglib.Artist, s.ArtistRaw)
	put(taglib.AlbumArtist, s.AlbumArtist)
	if len(s.AlbumArtists) > 0 {
		out[albumArtistsKey] = append([]string(nil), s.AlbumArtists...)
	}
	put(taglib.Date, s.Date)
	put(taglib.MusicBrainzTrackID, s.MBTrackID)
	put(taglib.MusicBrainzReleaseTrackID, s.MBReleaseTrackID)
	put(taglib.MusicBrainzAlbumID, s.MBAlbumID)
	put(taglib.MusicBrainzReleaseGroupID, s.MBReleaseGroupID)
	put(taglib.ReleaseType, s.ReleaseType)
	put(taglib.ReleaseStatus, s.ReleaseStatus)
	if len(s.Artists) > 0 {
		out[taglib.Artists] = s.Artists
	}
	if s.TrackNumber > 0 {
		out[taglib.TrackNumber] = []string{strconv.Itoa(s.TrackNumber)}
	}
	if s.DiscNumber > 0 {
		out[taglib.DiscNumber] = []string{strconv.Itoa(s.DiscNumber)}
	}
	if len(s.MBArtistIDs) > 0 {
		out[taglib.MusicBrainzArtistID] = append([]string(nil), s.MBArtistIDs...)
	}
	if len(s.MBAlbumArtistIDs) > 0 {
		out[taglib.MusicBrainzAlbumArtistID] = append([]string(nil), s.MBAlbumArtistIDs...)
	}
	return out
}

// WriteTags writes tags to path and normalizes MP3 TXXX descriptions to Picard casing.
func WriteTags(path string, tags map[string][]string) error {
	if err := taglib.WriteTags(path, tags, 0); err != nil {
		return fmt.Errorf("tags: write %q: %w", path, err)
	}
	if err := normalizeMP3TXXX(path); err != nil {
		return fmt.Errorf("tags: write %q: %w", path, err)
	}
	return nil
}

// ClearTags removes only LinkKeys from path, preserving all other frames, then normalizes MP3 TXXX.
func ClearTags(path string) error {
	keys := LinkKeys()

	raw, err := taglib.ReadTags(path)
	if err != nil {
		return fmt.Errorf("tags: read %q: %w", path, err)
	}
	for _, k := range keys {
		delete(raw, k)
	}
	if err := taglib.WriteTags(path, raw, taglib.Clear); err != nil {
		return fmt.Errorf("tags: clear %q: %w", path, err)
	}
	if err := normalizeMP3TXXX(path); err != nil {
		return fmt.Errorf("tags: clear %q: %w", path, err)
	}
	return nil
}
