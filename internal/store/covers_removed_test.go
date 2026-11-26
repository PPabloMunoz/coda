package store

import (
	"testing"

	apperrors "github.com/ppablomunoz/coda/internal/errors"
)

func TestCoverRemovedTombstone(t *testing.T) {
	st := newTestStore(t)

	// Removing a never-cached cover is a no-op that still records the
	// tombstone.
	if err := st.MarkCoverRemoved(ctx(), testRelID); err != nil {
		t.Fatalf("MarkCoverRemoved = %v", err)
	}
	row, err := st.GetCover(ctx(), testRelID)
	if err != nil {
		t.Fatalf("GetCover = %v", err)
	}
	if !row.Removed {
		t.Error("Removed = false, want true")
	}

	// An upsert (upload) clears the tombstone.
	if err := st.UpsertCover(ctx(), CoverParams{
		ReleaseID: testRelID, Source: CoverSourceUpload,
		MIME: "image/png", SizeBytes: 10,
	}); err != nil {
		t.Fatalf("UpsertCover = %v", err)
	}
	row, err = st.GetCover(ctx(), testRelID)
	if err != nil {
		t.Fatalf("GetCover after upsert = %v", err)
	}
	if row.Removed {
		t.Error("Removed still true after upsert, want false")
	}

	// Re-marking keeps the row removed.
	if err := st.MarkCoverRemoved(ctx(), testRelID); err != nil {
		t.Fatalf("MarkCoverRemoved again = %v", err)
	}
	row, err = st.GetCover(ctx(), testRelID)
	if err != nil {
		t.Fatalf("GetCover = %v", err)
	}
	if !row.Removed {
		t.Error("Removed = false, want true")
	}
}

func TestArtistCoverRemovedTombstone(t *testing.T) {
	st := newTestStore(t)

	if err := st.MarkArtistCoverRemoved(ctx(), testArtistID); err != nil {
		t.Fatalf("MarkArtistCoverRemoved = %v", err)
	}
	row, err := st.GetArtistCover(ctx(), testArtistID)
	if err != nil {
		t.Fatalf("GetArtistCover = %v", err)
	}
	if !row.Removed {
		t.Error("Removed = false, want true")
	}

	if err := st.UpsertArtistCover(ctx(), ArtistCoverParams{
		ArtistID: testArtistID, Source: ArtistCoverSourceUpload,
		MIME: "image/png", SizeBytes: 10,
	}); err != nil {
		t.Fatalf("UpsertArtistCover = %v", err)
	}
	row, err = st.GetArtistCover(ctx(), testArtistID)
	if err != nil {
		t.Fatalf("GetArtistCover after upsert = %v", err)
	}
	if row.Removed {
		t.Error("Removed still true after upsert, want false")
	}

	// Unknown ids stay unknown (no tombstone leaking into misses).
	if _, err := st.GetCover(ctx(), "00000000-0000-4000-8000-000000000000"); !apperrors.Is(err, apperrors.ErrNotFound) {
		t.Errorf("unknown release err = %v, want NOT_FOUND", err)
	}
	if _, err := st.GetArtistCover(ctx(), "00000000-0000-4000-8000-000000000000"); !apperrors.Is(err, apperrors.ErrNotFound) {
		t.Errorf("unknown artist err = %v, want NOT_FOUND", err)
	}
}
