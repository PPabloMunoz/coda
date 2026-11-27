package tasks

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/ppablomunoz/coda/internal/musicbrainz"
	"github.com/ppablomunoz/coda/internal/store"
	"github.com/ppablomunoz/coda/internal/utils"
)

func TestImportArtistDeletedMidFetchDoesNotResurrect(t *testing.T) {
	log := utils.NewWithWriter(io.Discard)
	st, err := store.NewStore(filepath.Join(t.TempDir(), "test.db"), nil, log)
	if err != nil {
		t.Fatalf("NewStore = %v", err)
	}
	t.Cleanup(st.Close)

	artistID := "d5b32d03-2aaf-4baf-ba5d-3721d095b833"
	if err := st.CreateArtist(context.Background(), store.Artist{ID: artistID, Name: "Main"}); err != nil {
		t.Fatalf("CreateArtist = %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/release", func(w http.ResponseWriter, r *http.Request) {
		// Simulate a user delete racing the MusicBrainz fetch: the runner
		// checked InLibrary before the fetch, the artist is gone now.
		if _, err := st.DeleteArtist(context.Background(), artistID, false); err != nil {
			t.Errorf("mid-fetch delete = %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"release-count": 1,
			"releases": []musicbrainz.Release{{
				ID: "33333333-4444-4555-b666-777777777777", Title: "Rel",
				Status: "Official",
				ReleaseGroup: musicbrainz.ReleaseGroup{
					ID: "11111111-2222-4333-8444-555555555555", Title: "Group", PrimaryType: "Album",
				},
				Media: []musicbrainz.Medium{{Position: 1, Tracks: []musicbrainz.ReleaseTrack{{
					ID: "55555555-6666-4777-b888-999999999999", Position: 1,
					Number: "1", Title: "Song",
					Recording: musicbrainz.RecordingRef{ID: "77777777-8888-4999-baaa-bbbbbbbbbbbb"},
				}}}},
			}},
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	mb, err := musicbrainz.New(musicbrainz.Config{UserAgent: "coda-test", BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("mb New = %v", err)
	}

	q := NewQueue(log)
	r := NewRunner(st, mb, log, q, nil, "")
	q.EnqueueRefreshArtist(artistID)
	r.exec(context.Background(), Task{Key: TaskImportDiscography, ID: artistID})

	if got := q.ArtistImportStatus(artistID); got.State != ArtistImportIdle {
		t.Errorf("state = %q, want idle (canceled import forgotten, not failed)", got.State)
	}
	if _, err := st.GetReleaseGroup(context.Background(), "11111111-2222-4333-8444-555555555555"); err == nil {
		t.Error("group resurrected after mid-fetch delete")
	}
}
