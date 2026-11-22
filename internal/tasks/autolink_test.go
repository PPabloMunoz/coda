package tasks

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	apperrors "github.com/ppablomunoz/coda/internal/errors"
	"github.com/ppablomunoz/coda/internal/tags"
)

func TestLibraryTargetPath(t *testing.T) {
	root := t.TempDir()
	got, err := LibraryTargetPath(root, &tags.LinkInfo{
		Title: "Song", Album: "Album",
		ArtistDisplay: "Artist", AlbumArtistDisplay: "Album Artist",
		TrackNumber: 1, DiscNumber: 2,
	}, "mp3")
	if err != nil {
		t.Fatalf("LibraryTargetPath = %v", err)
	}
	want := filepath.Join(root, "Album_Artist", "Album", "02-01_Song.mp3")
	if got != want {
		t.Errorf("path = %q, want %q", got, want)
	}
}

func TestLibraryTargetPathFallsBackToArtist(t *testing.T) {
	root := t.TempDir()
	got, err := LibraryTargetPath(root, &tags.LinkInfo{
		Title: "Song", Album: "Album", ArtistDisplay: "Artist",
		TrackNumber: 1, DiscNumber: 1,
	}, "flac")
	if err != nil {
		t.Fatalf("LibraryTargetPath = %v", err)
	}
	want := filepath.Join(root, "Artist", "Album", "01-01_Song.flac")
	if got != want {
		t.Errorf("path = %q, want %q", got, want)
	}
}

func TestLibraryTargetPathErrors(t *testing.T) {
	root := t.TempDir()
	if _, err := LibraryTargetPath(root, &tags.LinkInfo{
		Title: "Song", AlbumArtistDisplay: "Artist",
		TrackNumber: 1, DiscNumber: 1,
	}, "mp3"); err == nil {
		t.Error("empty album = nil, want error")
	}
}

func TestEnsureMoveDestination(t *testing.T) {
	root := t.TempDir()
	if err := EnsureMoveDestination("test", 1, "track", root+"/a.mp3", root+"/a.mp3"); err != nil {
		t.Errorf("same path = %v, want nil", err)
	}

	dst := filepath.Join(root, "sub", "dir", "song.mp3")
	if err := EnsureMoveDestination("test", 1, "track", root+"/a.mp3", dst); err != nil {
		t.Fatalf("new nested dst = %v, want nil", err)
	}
	if fi, err := os.Stat(filepath.Dir(dst)); err != nil || !fi.IsDir() {
		t.Errorf("parent dir not created: %v", err)
	}

	if err := os.WriteFile(dst, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := EnsureMoveDestination("test", 1, "track", root+"/a.mp3", dst); !apperrors.Is(err, apperrors.ErrAlreadyExists) {
		t.Errorf("occupied dst = %v, want ALREADY_EXISTS", err)
	}
}

func TestTrackIDFromTags(t *testing.T) {
	id := "55555555-6666-4777-b888-999999999999"
	if got, ok := trackIDFromTags(&tags.Snapshot{MBReleaseTrackID: id}); !ok || got != id {
		t.Errorf("valid = %q, %v; want %q, true", got, ok, id)
	}
	for _, snap := range []*tags.Snapshot{
		nil,
		{},
		{MBReleaseTrackID: "not-a-uuid"},
		{MBReleaseTrackID: "  "},
	} {
		if _, ok := trackIDFromTags(snap); ok {
			t.Errorf("snap %+v = true, want false", snap)
		}
	}
}

func TestEmbedCoverNoCoverIsNoop(t *testing.T) {
	r, _, _ := newTestRunner(t)
	if err := EmbedCover(context.Background(), r.st, "", "missing.mp3",
		"33333333-4444-4555-b666-777777777777"); err != nil {
		t.Errorf("no cached cover = %v, want nil", err)
	}
}

func TestAutoLinkMovesEmptyDB(t *testing.T) {
	r, _, _ := newTestRunner(t)
	stats := r.autoLinkMoves(context.Background())
	if stats != (AutoLinkStats{}) {
		t.Errorf("empty db stats = %+v, want zero", stats)
	}
}
