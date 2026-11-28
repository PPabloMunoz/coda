package tasks

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/ppablomunoz/coda/internal/musicbrainz"
	"github.com/ppablomunoz/coda/internal/store"
	"github.com/ppablomunoz/coda/internal/utils"
)

func TestImportEnqueuesPrefetchTasks(t *testing.T) {
	const (
		artistID = "d5b32d03-2aaf-4baf-ba5d-3721d095b833"
		rel1     = "33333333-4444-4555-b666-777777777777"
		group1   = "11111111-2222-4333-8444-555555555555"
		track1   = "55555555-6666-4777-b888-999999999999"
		rec1     = "77777777-8888-4999-baaa-bbbbbbbbbbbb"
		rel2     = "44444444-5555-4666-b777-888888888888"
		group2   = "22222222-3333-4444-a555-666666666666"
		track2   = "66666666-7777-4888-b999-aaaaaaaaaaaa"
		rec2     = "88888888-9999-4aaa-bbbb-cccccccccccc"
	)
	mkRelease := func(id, group, track, rec, title string) musicbrainz.Release {
		credit := []musicbrainz.ArtistCredit{{
			Name: "Test", Artist: musicbrainz.Artist{ID: artistID, Name: "Test"},
		}}
		return musicbrainz.Release{
			ID: id, Title: title, Status: "Official", ArtistCredit: credit,
			ReleaseGroup: musicbrainz.ReleaseGroup{ID: group, Title: title + " Group", PrimaryType: "Album", ArtistCredit: credit},
			Media: []musicbrainz.Medium{{Position: 1, Tracks: []musicbrainz.ReleaseTrack{{
				ID: track, Position: 1, Number: "1", Title: title + " Track",
				Recording: musicbrainz.RecordingRef{ID: rec},
			}}}},
		}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"release-count": 2,
			"releases": []musicbrainz.Release{
				mkRelease(rel1, group1, track1, rec1, "First"),
				mkRelease(rel2, group2, track2, rec2, "Second"),
			},
		})
	}))
	t.Cleanup(srv.Close)

	log := utils.NewWithWriter(io.Discard)
	st, err := store.NewStore(filepath.Join(t.TempDir(), "test.db"), nil, log)
	if err != nil {
		t.Fatalf("NewStore = %v", err)
	}
	t.Cleanup(st.Close)
	if err := st.CreateArtist(context.Background(), store.Artist{ID: artistID, Name: "Test"}); err != nil {
		t.Fatalf("CreateArtist = %v", err)
	}
	mb, err := musicbrainz.New(musicbrainz.Config{UserAgent: "coda-test", BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("mb New = %v", err)
	}
	// Non-nil art enables enqueueing; no fetch happens until the queued
	// tasks run, so the default provider URLs are never hit here.
	art := newTestFetcher(t, srv.URL)
	q := NewQueue(log, st)
	r := NewRunner(st, mb, log, q, art, t.TempDir())
	r.baseCtx = context.Background()

	q.EnqueueRefreshArtist(artistID)
	r.exec(context.Background(), Task{Key: TaskImportDiscography, ID: artistID})

	if got := q.ArtistImportStatus(artistID); got.State != ArtistImportDone {
		t.Fatalf("import state = %q, want done", got.State)
	}
	if !q.HasTask(TaskPrefetchCover, rel1) || !q.HasTask(TaskPrefetchCover, rel2) {
		t.Error("missing prefetch cover tasks for both releases after import")
	}
	if !q.HasTask(TaskPrefetchArtistCover, artistID) {
		t.Error("missing prefetch artist cover task after import")
	}
}

func TestPrefetchCoverTask(t *testing.T) {
	root := t.TempDir()
	r, st := newEmbedRunner(t, root)
	seedEmbedRelease(t, st)
	ctx := context.Background()

	var calls atomic.Int64
	srv := missTestServer(t, &calls, "art")
	r.art = newTestFetcher(t, srv.URL)

	r.prefetchCover(ctx, embedRelID)
	if _, err := st.GetCover(ctx, embedRelID); err != nil {
		t.Fatalf("GetCover after prefetch = %v, want cached row", err)
	}
	if calls.Load() == 0 {
		t.Error("prefetch made no provider calls")
	}
}

func TestPrefetchCoverSkipsMissingRelease(t *testing.T) {
	root := t.TempDir()
	r, st := newEmbedRunner(t, root)
	seedEmbedRelease(t, st)
	ctx := context.Background()

	var calls atomic.Int64
	srv := missTestServer(t, &calls, "notFound")
	r.art = newTestFetcher(t, srv.URL)

	r.prefetchCover(ctx, "99999999-0000-4000-8000-000000000000")
	if got := calls.Load(); got != 0 {
		t.Errorf("provider calls for missing release = %d, want 0 (no fetch without a stored release)", got)
	}
}
