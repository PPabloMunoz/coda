package store

import (
	"context"
	"io"
	"path/filepath"
	"testing"

	"github.com/ppablomunoz/coda/internal/utils"
)

func newTestStore(t *testing.T, roots ...string) *Store {
	t.Helper()
	return newStoreAt(t, filepath.Join(t.TempDir(), "test.db"), roots...)
}

func newStoreAt(t *testing.T, dbPath string, roots ...string) *Store {
	t.Helper()
	dsn := dbPath
	log := utils.NewWithWriter(io.Discard)
	st, err := NewStore(dsn, roots, log)
	if err != nil {
		t.Fatalf("NewStore = %v", err)
	}
	t.Cleanup(st.Close)
	if err := st.AddRootDirs(context.Background()); err != nil {
		t.Fatalf("AddRootDirs = %v", err)
	}
	return st
}

func ctx() context.Context { return context.Background() }

// Test UUIDs (36-char lowercase hex).
const (
	testArtistID = "d5b32d03-2aaf-4baf-ba5d-3721d095b833"
	testArtistB  = "a1b2c3d4-e5f6-47ab-8cd0-ef1234567890"
	testGroupID  = "11111111-2222-4333-8444-555555555555"
	testGroupEP  = "22222222-3333-4444-a555-666666666666"
	testRelID    = "33333333-4444-4555-b666-777777777777"
	testRelID2   = "44444444-5555-4666-b777-888888888888"
	testTrackID  = "55555555-6666-4777-b888-999999999999"
	testTrackID2 = "66666666-7777-4888-b999-aaaaaaaaaaaa"
	testRecID    = "77777777-8888-4999-baaa-bbbbbbbbbbbb"
)

func testCredit(id, name string) []CreditEntry {
	return []CreditEntry{{ArtistID: id, Name: name, CreditedName: name}}
}

func testRelease(trackID, releaseID, groupID string) FullReleaseParams {
	return FullReleaseParams{
		ID:    releaseID,
		Title: "Test Release",
		Credit: testCredit(
			testArtistID,
			"Test Artist",
		),
		Group: ReleaseGroupParams{
			ID:     groupID,
			Title:  "Test Group",
			Type:   "album",
			Credit: testCredit(testArtistID, "Test Artist"),
		},
		Tracks: []TrackParams{
			{
				ID:          trackID,
				ReleaseID:   releaseID,
				Disc:        1,
				Position:    1,
				Number:      "1",
				Title:       "Test Track",
				Credit:      testCredit(testArtistID, "Test Artist"),
				RecordingID: testRecID,
			},
		},
	}
}
