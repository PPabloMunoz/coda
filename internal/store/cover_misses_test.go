package store

import (
	"testing"
	"time"

	apperrors "github.com/ppablomunoz/coda/internal/errors"
)

func seedMissRelease(t *testing.T, st *Store) {
	t.Helper()
	if err := st.CreateArtist(ctx(), Artist{ID: testArtistID, Name: "Test Artist"}); err != nil {
		t.Fatalf("CreateArtist = %v", err)
	}
	if err := st.SaveDiscography(ctx(), []FullReleaseParams{testRelease(testTrackID, testRelID, testGroupID)}); err != nil {
		t.Fatalf("SaveDiscography = %v", err)
	}
}

func TestReleaseCoverMissRoundTrip(t *testing.T) {
	st := newTestStore(t)
	seedMissRelease(t, st)

	if _, err := st.GetReleaseCoverMiss(ctx(), testRelID); !apperrors.Is(err, apperrors.ErrNotFound) {
		t.Fatalf("Get miss before record = %v, want NOT_FOUND", err)
	}

	now := time.Now().UTC()
	if err := st.UpsertReleaseCoverMiss(ctx(), testRelID, 1, now, now.Add(6*time.Hour)); err != nil {
		t.Fatalf("Upsert miss = %v", err)
	}
	m, err := st.GetReleaseCoverMiss(ctx(), testRelID)
	if err != nil {
		t.Fatalf("Get miss = %v", err)
	}
	if m.ID != testRelID || m.Misses != 1 {
		t.Errorf("miss = %+v, want id %q misses 1", m, testRelID)
	}
	if !m.Active(now) {
		t.Error("miss not active right after record, want active")
	}
	if m.Active(now.Add(7 * time.Hour)) {
		t.Error("miss still active after retry_after, want expired")
	}

	// Second strike overwrites.
	later := now.Add(time.Minute)
	if err := st.UpsertReleaseCoverMiss(ctx(), testRelID, 2, later, later.Add(12*time.Hour)); err != nil {
		t.Fatalf("Upsert miss 2 = %v", err)
	}
	m, err = st.GetReleaseCoverMiss(ctx(), testRelID)
	if err != nil {
		t.Fatalf("Get miss 2 = %v", err)
	}
	if m.Misses != 2 {
		t.Errorf("misses = %d, want 2", m.Misses)
	}

	if err := st.DeleteReleaseCoverMiss(ctx(), testRelID); err != nil {
		t.Fatalf("Delete miss = %v", err)
	}
	if _, err := st.GetReleaseCoverMiss(ctx(), testRelID); !apperrors.Is(err, apperrors.ErrNotFound) {
		t.Errorf("Get after delete = %v, want NOT_FOUND", err)
	}
	// Deleting again is a no-op.
	if err := st.DeleteReleaseCoverMiss(ctx(), testRelID); err != nil {
		t.Errorf("Delete missing = %v, want nil", err)
	}
}

func TestArtistCoverMissRoundTrip(t *testing.T) {
	st := newTestStore(t)
	if err := st.CreateArtist(ctx(), Artist{ID: testArtistID, Name: "Test Artist"}); err != nil {
		t.Fatalf("CreateArtist = %v", err)
	}

	now := time.Now().UTC()
	if err := st.UpsertArtistCoverMiss(ctx(), testArtistID, 1, now, now.Add(6*time.Hour)); err != nil {
		t.Fatalf("Upsert artist miss = %v", err)
	}
	m, err := st.GetArtistCoverMiss(ctx(), testArtistID)
	if err != nil {
		t.Fatalf("Get artist miss = %v", err)
	}
	if m.ID != testArtistID || m.Misses != 1 {
		t.Errorf("miss = %+v, want id %q misses 1", m, testArtistID)
	}
	if !m.Active(now) {
		t.Error("artist miss not active, want active")
	}
	if err := st.DeleteArtistCoverMiss(ctx(), testArtistID); err != nil {
		t.Fatalf("Delete artist miss = %v", err)
	}
	if _, err := st.GetArtistCoverMiss(ctx(), testArtistID); !apperrors.Is(err, apperrors.ErrNotFound) {
		t.Errorf("Get after delete = %v, want NOT_FOUND", err)
	}
}

func TestCoverMissClearedByCoverWrites(t *testing.T) {
	st := newTestStore(t)
	seedMissRelease(t, st)
	now := time.Now().UTC()

	if err := st.UpsertReleaseCoverMiss(ctx(), testRelID, 3, now, now.Add(24*time.Hour)); err != nil {
		t.Fatalf("Upsert miss = %v", err)
	}
	// Upload clears the miss.
	if err := st.UpsertCover(ctx(), CoverParams{
		ReleaseID: testRelID, Source: CoverSourceUpload, MIME: "image/png", SizeBytes: 10,
	}); err != nil {
		t.Fatalf("UpsertCover = %v", err)
	}
	if _, err := st.GetReleaseCoverMiss(ctx(), testRelID); !apperrors.Is(err, apperrors.ErrNotFound) {
		t.Errorf("miss after upsert = %v, want NOT_FOUND", err)
	}

	if err := st.UpsertReleaseCoverMiss(ctx(), testRelID, 1, now, now.Add(6*time.Hour)); err != nil {
		t.Fatalf("Upsert miss again = %v", err)
	}
	// Removal clears the miss (the tombstone alone suppresses fetches).
	if err := st.MarkCoverRemoved(ctx(), testRelID); err != nil {
		t.Fatalf("MarkCoverRemoved = %v", err)
	}
	if _, err := st.GetReleaseCoverMiss(ctx(), testRelID); !apperrors.Is(err, apperrors.ErrNotFound) {
		t.Errorf("miss after remove = %v, want NOT_FOUND", err)
	}

	if err := st.UpsertReleaseCoverMiss(ctx(), testRelID, 1, now, now.Add(6*time.Hour)); err != nil {
		t.Fatalf("Upsert miss 3rd = %v", err)
	}
	// Stale-row delete clears the miss so the next GET retries.
	if err := st.DeleteCover(ctx(), testRelID); err != nil {
		t.Fatalf("DeleteCover = %v", err)
	}
	if _, err := st.GetReleaseCoverMiss(ctx(), testRelID); !apperrors.Is(err, apperrors.ErrNotFound) {
		t.Errorf("miss after delete = %v, want NOT_FOUND", err)
	}
}

func TestArtistMissClearedByCoverWrites(t *testing.T) {
	st := newTestStore(t)
	if err := st.CreateArtist(ctx(), Artist{ID: testArtistID, Name: "Test Artist"}); err != nil {
		t.Fatalf("CreateArtist = %v", err)
	}
	now := time.Now().UTC()

	if err := st.UpsertArtistCoverMiss(ctx(), testArtistID, 1, now, now.Add(6*time.Hour)); err != nil {
		t.Fatalf("Upsert artist miss = %v", err)
	}
	if err := st.UpsertArtistCover(ctx(), ArtistCoverParams{
		ArtistID: testArtistID, Source: ArtistCoverSourceUpload, MIME: "image/png", SizeBytes: 10,
	}); err != nil {
		t.Fatalf("UpsertArtistCover = %v", err)
	}
	if _, err := st.GetArtistCoverMiss(ctx(), testArtistID); !apperrors.Is(err, apperrors.ErrNotFound) {
		t.Errorf("artist miss after upsert = %v, want NOT_FOUND", err)
	}

	if err := st.UpsertArtistCoverMiss(ctx(), testArtistID, 1, now, now.Add(6*time.Hour)); err != nil {
		t.Fatalf("Upsert artist miss again = %v", err)
	}
	if err := st.MarkArtistCoverRemoved(ctx(), testArtistID); err != nil {
		t.Fatalf("MarkArtistCoverRemoved = %v", err)
	}
	if _, err := st.GetArtistCoverMiss(ctx(), testArtistID); !apperrors.Is(err, apperrors.ErrNotFound) {
		t.Errorf("artist miss after remove = %v, want NOT_FOUND", err)
	}

	if err := st.UpsertArtistCoverMiss(ctx(), testArtistID, 1, now, now.Add(6*time.Hour)); err != nil {
		t.Fatalf("Upsert artist miss 3rd = %v", err)
	}
	if err := st.DeleteArtistCover(ctx(), testArtistID); err != nil {
		t.Fatalf("DeleteArtistCover = %v", err)
	}
	if _, err := st.GetArtistCoverMiss(ctx(), testArtistID); !apperrors.Is(err, apperrors.ErrNotFound) {
		t.Errorf("artist miss after delete = %v, want NOT_FOUND", err)
	}
}
