package tasks

import (
	"context"
	"testing"

	"github.com/ppablomunoz/coda/internal/store"
	"github.com/ppablomunoz/coda/internal/tags"
)

func TestEnqueueRelinkReleaseDedup(t *testing.T) {
	q := NewQueue(nil)
	q.EnqueueRelinkRelease("rel-1")
	q.EnqueueRelinkRelease("rel-1")
	q.EnqueueRelinkRelease("rel-2")

	q.mu.Lock()
	defer q.mu.Unlock()
	n := 0
	for _, item := range q.items {
		if item.Key == TaskRelinkRelease && item.ID == "rel-1" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("relink tasks for rel-1 = %d, want 1 (dedup)", n)
	}
	if len(q.items) != 2 {
		t.Errorf("queued = %d, want 2 (rel-1 + rel-2)", len(q.items))
	}
}

func TestRelinkReleaseRewritesTags(t *testing.T) {
	const (
		newRelID   = "44444444-5555-4666-b777-888888888888"
		newTrackID = "66666666-7777-4888-b999-aaaaaaaaaaaa"
		recShared  = "77777777-8888-4999-baaa-bbbbbbbbbbbb"
	)
	root := t.TempDir()
	r, st := newEmbedRunner(t, root)
	seedEmbedRelease(t, st)
	ctx := context.Background()

	id, _ := syncWavFile(t, st, root, "song.wav")
	if _, _, err := LinkFile(ctx, st, r.coverDir, id, embedTrackID); err != nil {
		t.Fatalf("LinkFile old = %v", err)
	}
	row := fileIDRow(t, st, id)
	snap, err := tags.Read(row.Path)
	if err != nil {
		t.Fatalf("Read before switch = %v", err)
	}
	if snap.MBReleaseTrackID != embedTrackID {
		t.Fatalf("before MBReleaseTrackID = %q, want %q", snap.MBReleaseTrackID, embedTrackID)
	}

	credit := []store.CreditEntry{{ArtistID: embedArtistID, Name: "Embed Artist", CreditedName: "Embed Artist"}}
	replacement := store.FullReleaseParams{
		ID: newRelID, Title: "Embed Release", Credit: credit,
		Group: store.ReleaseGroupParams{ID: embedGroupID, Title: "Embed Group", Type: "album", Credit: credit},
		Tracks: []store.TrackParams{{
			ID: newTrackID, ReleaseID: newRelID,
			Disc: 1, Position: 1, Number: "1", Title: "Embed Track",
			Credit: credit, RecordingID: recShared,
		}},
	}
	if err := st.ReplaceRelease(ctx, embedGroupID, replacement); err != nil {
		t.Fatalf("ReplaceRelease = %v", err)
	}
	afterSwitch := fileIDRow(t, st, id)
	if afterSwitch.TrackID == nil || *afterSwitch.TrackID != newTrackID {
		t.Fatalf("after switch track = %v, want %q (DB remapped)", afterSwitch.TrackID, newTrackID)
	}

	r.relinkRelease(ctx, newRelID)

	final := fileIDRow(t, st, id)
	if final.TrackID == nil || *final.TrackID != newTrackID {
		t.Fatalf("after relink track = %v, want %q", final.TrackID, newTrackID)
	}
	got, err := tags.Read(final.Path)
	if err != nil {
		t.Fatalf("Read after relink = %v", err)
	}
	if got.MBReleaseTrackID != newTrackID {
		t.Errorf("after MBReleaseTrackID = %q, want %q", got.MBReleaseTrackID, newTrackID)
	}
	if got.MBAlbumID != newRelID {
		t.Errorf("after MBAlbumID = %q, want %q", got.MBAlbumID, newRelID)
	}
}

func TestExecRelinkReleaseNoPanic(t *testing.T) {
	r, _, _ := newTestRunner(t)
	r.exec(context.Background(), Task{Key: TaskRelinkRelease, ID: embedRelID})
	r.exec(context.Background(), Task{Key: TaskRelinkRelease, ID: "bad"})
}
