package tasks

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ppablomunoz/coda/internal/musicbrainz"
	"github.com/ppablomunoz/coda/internal/store"
	"github.com/ppablomunoz/coda/internal/utils"
)

// importDiscography fetches official albums/EPs/singles with tracks from MusicBrainz
// and saves them, logging fetch vs save time.
func (r *Runner) importDiscography(ctx context.Context, id string) error {
	mbStart := time.Now()
	mbReleases, err := r.mb.GetReleasesWithTracksByArtist(ctx, id)
	if err != nil {
		return fmt.Errorf("discography releases: %w", err)
	}
	mbTook := time.Since(mbStart)

	releases := []store.FullReleaseParams{}
	for _, r := range mbReleases {
		releases = append(releases, FullReleaseFromMB(r))
	}

	saveStart := time.Now()
	if err := r.st.SaveDiscography(ctx, releases); err != nil {
		return err
	}
	r.log.Infof("discography for %q: musicbrainz %s, save %s (%d releases)",
		id, mbTook.Round(time.Millisecond), time.Since(saveStart).Round(time.Millisecond), len(releases))

	return nil
}

// ArtistFromMB converts a MusicBrainz artist to its store form.
// Single owner for MB→store artist mapping (server reuses this).
func ArtistFromMB(m *musicbrainz.Artist) store.Artist {
	return store.Artist{
		ID:             m.ID,
		Name:           m.Name,
		SortName:       utils.NullIfEmpty(m.SortName),
		Disambiguation: utils.NullIfEmpty(m.Disambiguation),
		Active:         1,
		InLibrary:      1,
	}
}

// MBCreditToStore converts MusicBrainz credits to store entries, preserving join phrases.
func MBCreditToStore(c []musicbrainz.ArtistCredit) []store.CreditEntry {
	if len(c) == 0 {
		return nil
	}
	out := make([]store.CreditEntry, 0, len(c))
	for _, e := range c {
		out = append(out, store.CreditEntry{
			ArtistID:       e.Artist.ID,
			Name:           e.Artist.Name,
			SortName:       utils.NullIfEmpty(e.Artist.SortName),
			Disambiguation: utils.NullIfEmpty(e.Artist.Disambiguation),
			CreditedName:   e.Name,
			JoinPhrase:     e.JoinPhrase,
		})
	}
	return out
}

// FullReleaseFromMB converts a MusicBrainz release with media into store params.
func FullReleaseFromMB(r musicbrainz.Release) store.FullReleaseParams {
	p := store.FullReleaseParams{
		ID:     r.ID,
		Title:  r.Title,
		Date:   utils.NullIfEmpty(r.Date),
		Status: toLowerIfNotNull(utils.NullIfEmpty(r.Status)),
		Credit: MBCreditToStore(r.ArtistCredit),
		Group: store.ReleaseGroupParams{
			ID:     r.ReleaseGroup.ID,
			Title:  r.ReleaseGroup.Title,
			Type:   r.ReleaseGroup.PrimaryType,
			Credit: MBCreditToStore(r.ReleaseGroup.ArtistCredit),
		},
	}
	for _, m := range r.Media {
		for _, t := range m.Tracks {
			p.Tracks = append(p.Tracks, store.TrackParams{
				ID:          t.ID,
				Disc:        m.Position,
				Position:    t.Position,
				Number:      t.Number,
				Title:       t.Title,
				Credit:      MBCreditToStore(t.ArtistCredit),
				DurMS:       t.Length,
				RecordingID: t.Recording.ID,
			})
		}
	}
	return p
}

// toLowerIfNotNull lowercases s for DB storage, preserving nil.
func toLowerIfNotNull(s *string) *string {
	if s != nil {
		lower := strings.ToLower(*s)
		return &lower
	}
	return nil
}
