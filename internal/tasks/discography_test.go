package tasks

import (
	"testing"

	"github.com/ppablomunoz/coda/internal/musicbrainz"
)

func TestArtistFromMB(t *testing.T) {
	got := ArtistFromMB(&musicbrainz.Artist{
		ID: "d5b32d03-2aaf-4baf-ba5d-3721d095b833", Name: "Test",
		SortName: "Test, The", Disambiguation: "band",
	})
	if got.ID == "" || got.Name != "Test" || got.Active != 1 || got.InLibrary != 1 {
		t.Errorf("got %+v", got)
	}
	if got.SortName == nil || *got.SortName != "Test, The" {
		t.Errorf("sort = %v", got.SortName)
	}
	// Empty optionals become nil.
	got = ArtistFromMB(&musicbrainz.Artist{ID: "x", Name: "Y"})
	if got.SortName != nil || got.Disambiguation != nil {
		t.Errorf("empty optionals should be nil: %+v", got)
	}
}

func TestMBCreditToStore(t *testing.T) {
	if out := MBCreditToStore(nil); out != nil {
		t.Errorf("nil = %v, want nil", out)
	}
	out := MBCreditToStore([]musicbrainz.ArtistCredit{{
		Name: "Credited", JoinPhrase: " & ",
		Artist: musicbrainz.Artist{ID: "id-1", Name: "Real"},
	}})
	if len(out) != 1 {
		t.Fatalf("len = %d", len(out))
	}
	if out[0].ArtistID != "id-1" || out[0].CreditedName != "Credited" || out[0].JoinPhrase != " & " {
		t.Errorf("got %+v", out[0])
	}
}

func TestFullReleaseFromMB(t *testing.T) {
	length := 200000
	r := musicbrainz.Release{
		ID: "33333333-4444-4555-b666-777777777777", Title: "Rel",
		Date: "2024-01-01", Status: "Official",
		ArtistCredit: []musicbrainz.ArtistCredit{{
			Name: "A", Artist: musicbrainz.Artist{ID: "d5b32d03-2aaf-4baf-ba5d-3721d095b833", Name: "A"},
		}},
		ReleaseGroup: musicbrainz.ReleaseGroup{
			ID: "11111111-2222-4333-8444-555555555555", Title: "Group", PrimaryType: "Album",
		},
		Media: []musicbrainz.Medium{{
			Position: 2, Tracks: []musicbrainz.ReleaseTrack{{
				ID: "55555555-6666-4777-b888-999999999999", Position: 1,
				Number: "1", Title: "Song", Length: &length,
				Recording: musicbrainz.RecordingRef{ID: "77777777-8888-4999-baaa-bbbbbbbbbbbb"},
			}},
		}},
	}
	got := FullReleaseFromMB(r)
	if got.ID != r.ID || got.Title != "Rel" {
		t.Errorf("got %+v", got)
	}
	if got.Date == nil || *got.Date != "2024-01-01" {
		t.Errorf("date = %v", got.Date)
	}
	if got.Status == nil || *got.Status != "official" {
		t.Errorf("status should be lowercased, got %v", got.Status)
	}
	if len(got.Tracks) != 1 || got.Tracks[0].Disc != 2 {
		t.Errorf("tracks = %+v", got.Tracks)
	}
	if got.Tracks[0].DurMS == nil || *got.Tracks[0].DurMS != length {
		t.Errorf("dur = %v", got.Tracks[0].DurMS)
	}

	// Nil status stays nil.
	r.Status = ""
	if got := FullReleaseFromMB(r); got.Status != nil {
		t.Errorf("empty status should be nil, got %v", got.Status)
	}
}

func TestToLowerIfNotNull(t *testing.T) {
	if out := toLowerIfNotNull(nil); out != nil {
		t.Errorf("nil = %v", out)
	}
	s := "Official"
	if out := toLowerIfNotNull(&s); out == nil || *out != "official" {
		t.Errorf("got %v", out)
	}
}

func TestFullReleaseFromMBStoreRoundTrip(t *testing.T) {
	// Mapping must produce something SaveDiscography-shaped.
	r := musicbrainz.Release{
		ID: "33333333-4444-4555-b666-777777777777", Title: "Rel",
		ReleaseGroup: musicbrainz.ReleaseGroup{
			ID: "11111111-2222-4333-8444-555555555555", Title: "G", PrimaryType: "Single",
		},
		Media: []musicbrainz.Medium{{Position: 1, Tracks: []musicbrainz.ReleaseTrack{
			{ID: "55555555-6666-4777-b888-999999999999", Position: 1, Number: "1", Title: "T"},
		}}},
	}
	got := FullReleaseFromMB(r)
	if got.Group.Type != "Single" {
		t.Errorf("type = %q", got.Group.Type)
	}
}
