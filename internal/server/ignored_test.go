package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/ppablomunoz/coda/internal/musicbrainz"
	"github.com/ppablomunoz/coda/internal/store"
)

func TestIgnoredFlow(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	artistID := "d5b32d03-2aaf-4baf-ba5d-3721d095b833"
	groupID := "11111111-2222-4333-8444-555555555555"
	relID := "33333333-4444-4555-b666-777777777777"
	trackID := "55555555-6666-4777-b888-999999999999"

	credit := []store.CreditEntry{{ArtistID: artistID, Name: "Artist", CreditedName: "Artist"}}
	if err := env.st.CreateArtist(ctx, store.Artist{ID: artistID, Name: "Artist"}); err != nil {
		t.Fatalf("CreateArtist = %v", err)
	}
	if err := env.st.SaveDiscography(ctx, []store.FullReleaseParams{{
		ID: relID, Title: "Rel", Credit: credit,
		Group: store.ReleaseGroupParams{ID: groupID, Title: "Group", Type: "album", Credit: credit},
		Tracks: []store.TrackParams{{
			ID: trackID, ReleaseID: relID, Disc: 1, Position: 1,
			Number: "1", Title: "Song", Credit: credit, RecordingID: "77777777-8888-4999-baaa-bbbbbbbbbbbb",
		}},
	}}); err != nil {
		t.Fatalf("SaveDiscography = %v", err)
	}

	// Invalid permanent value.
	w := doRequest(t, env.router, "DELETE", "/api/v1/release-groups/"+groupID+"?permanent=maybe", nil)
	if w.Code != http.StatusBadRequest {
		t.Errorf("bad permanent = %d, want 400", w.Code)
	}

	// Normal delete recreates on refresh; permanent delete sticks.
	w = doRequest(t, env.router, "DELETE", "/api/v1/release-groups/"+groupID, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("delete group = %d", w.Code)
	}
	w = doRequest(t, env.router, "DELETE", "/api/v1/release-groups/"+groupID+"?permanent=true", nil)
	if w.Code != http.StatusNotFound {
		t.Errorf("second delete = %d, want 404", w.Code)
	}

	// Re-seed then permanently delete.
	if err := env.st.SaveDiscography(ctx, []store.FullReleaseParams{{
		ID: relID, Title: "Rel", Credit: credit,
		Group: store.ReleaseGroupParams{ID: groupID, Title: "Group", Type: "album", Credit: credit},
		Tracks: []store.TrackParams{{
			ID: trackID, ReleaseID: relID, Disc: 1, Position: 1,
			Number: "1", Title: "Song", Credit: credit, RecordingID: "77777777-8888-4999-baaa-bbbbbbbbbbbb",
		}},
	}}); err != nil {
		t.Fatalf("re-seed = %v", err)
	}
	w = doRequest(t, env.router, "DELETE", "/api/v1/release-groups/"+groupID+"?permanent=true", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("permanent delete group = %d", w.Code)
	}

	// Ignored list contains the group.
	w = doRequest(t, env.router, "GET", "/api/v1/ignored", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list ignored = %d", w.Code)
	}
	var list struct {
		Total int                  `json:"total"`
		Items []store.IgnoredEntry `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode ignored = %v", err)
	}
	if list.Total != 1 || list.Items[0].ID != groupID {
		t.Fatalf("ignored = %+v, want the group", list)
	}

	// Stats include the tombstone.
	w = doRequest(t, env.router, "GET", "/api/v1/stats", nil)
	var stats map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &stats); err != nil {
		t.Fatalf("decode stats = %v", err)
	}
	if stats["ignored"] != float64(1) {
		t.Errorf("stats ignored = %v, want 1", stats["ignored"])
	}

	// Removing the tombstone allows re-import (nothing auto-refreshes).
	w = doRequest(t, env.router, "DELETE", "/api/v1/ignored/"+groupID, nil)
	if w.Code != http.StatusNoContent {
		t.Errorf("remove ignored = %d, want 204", w.Code)
	}
	w = doRequest(t, env.router, "DELETE", "/api/v1/ignored/"+groupID, nil)
	if w.Code != http.StatusNotFound {
		t.Errorf("second remove = %d, want 404", w.Code)
	}

	// Permanently delete the artist, then re-adding is forbidden.
	w = doRequest(t, env.router, "DELETE", "/api/v1/artists/"+artistID+"?permanent=true", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("permanent delete artist = %d", w.Code)
	}
	env.mbMux.HandleFunc("/artist/"+artistID, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(musicbrainz.Artist{ID: artistID, Name: "Artist"})
	})
	w = doRequest(t, env.router, "POST", "/api/v1/artists", map[string]string{"id": artistID})
	if w.Code != http.StatusForbidden {
		t.Errorf("re-add ignored artist = %d, want 403", w.Code)
	}

	// Restore the artist tombstone, re-add works again.
	w = doRequest(t, env.router, "DELETE", "/api/v1/ignored/"+artistID, nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("restore artist = %d", w.Code)
	}
	w = doRequest(t, env.router, "POST", "/api/v1/artists", map[string]string{"id": artistID})
	if w.Code != http.StatusCreated {
		t.Errorf("re-add after restore = %d, want 201 (%s)", w.Code, w.Body.String())
	}

	// Invalid type filter.
	w = doRequest(t, env.router, "GET", "/api/v1/ignored?type=bogus", nil)
	if w.Code != http.StatusBadRequest {
		t.Errorf("bad type = %d, want 400", w.Code)
	}
}
