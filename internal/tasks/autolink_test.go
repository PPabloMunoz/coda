package tasks

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/ppablomunoz/coda/internal/tags"
)

func TestLibraryTargetPath(t *testing.T) {
	root := t.TempDir()
	got, err := LibraryTargetPath(root, &tags.LinkInfo{
		Title: "Song", Album: "Album",
		ArtistDisplay: "Artist", AlbumArtistDisplay: "Album Artist",
		TrackNumber: 1, DiscNumber: 2, DiscTotal: 3,
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
		TrackNumber: 1, DiscNumber: 1, DiscTotal: 1,
	}, "flac")
	if err != nil {
		t.Fatalf("LibraryTargetPath = %v", err)
	}
	want := filepath.Join(root, "Artist", "Album", "01_Song.flac")
	if got != want {
		t.Errorf("path = %q, want %q", got, want)
	}
}

func TestLibraryTargetPathErrors(t *testing.T) {
	root := t.TempDir()
	if _, err := LibraryTargetPath(root, &tags.LinkInfo{
		Title: "Song", AlbumArtistDisplay: "Artist",
		TrackNumber: 1, DiscNumber: 1, DiscTotal: 1,
	}, "mp3"); err == nil {
		t.Error("empty album = nil, want error")
	}
}

func TestLibraryTargetPathOmitsDiscPrefixWhenTotalUnknown(t *testing.T) {
	root := t.TempDir()
	got, err := LibraryTargetPath(root, &tags.LinkInfo{
		Title: "Song", Album: "Album", ArtistDisplay: "Artist",
		TrackNumber: 2, DiscNumber: 1,
	}, "mp3")
	if err != nil {
		t.Fatalf("LibraryTargetPath = %v, want nil", err)
	}
	want := filepath.Join(root, "Artist", "Album", "02_Song.mp3")
	if got != want {
		t.Errorf("path = %q, want %q", got, want)
	}
}

func TestLibraryTargetPathUsesFirstGroupArtist(t *testing.T) {
	root := t.TempDir()
	got, err := LibraryTargetPath(root, &tags.LinkInfo{
		Title: "Song", Album: "Album",
		AlbumArtistDisplay: "Alice & Bob",
		AlbumArtists:       []string{"Alice", "Bob"},
		FolderArtist:       "Alice",
		TrackNumber:        1, DiscNumber: 1, DiscTotal: 1,
	}, "mp3")
	if err != nil {
		t.Fatalf("LibraryTargetPath = %v, want nil", err)
	}
	want := filepath.Join(root, "Alice", "Album", "01_Song.mp3")
	if got != want {
		t.Errorf("path = %q, want %q", got, want)
	}
}

func TestLibraryTargetPathFallsBackWithoutFolderArtist(t *testing.T) {
	root := t.TempDir()
	got, err := LibraryTargetPath(root, &tags.LinkInfo{
		Title: "Song", Album: "Album",
		AlbumArtistDisplay: "Alice & Bob",
		AlbumArtists:       []string{"Alice", "Bob"},
		TrackNumber:        1, DiscNumber: 1, DiscTotal: 1,
	}, "mp3")
	if err != nil {
		t.Fatalf("LibraryTargetPath = %v, want nil", err)
	}
	want := filepath.Join(root, "Alice_&_Bob", "Album", "01_Song.mp3")
	if got != want {
		t.Errorf("path = %q, want %q", got, want)
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
