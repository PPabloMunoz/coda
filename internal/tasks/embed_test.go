package tasks

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"testing"

	"go.senan.xyz/taglib"

	"github.com/ppablomunoz/coda/internal/artwork"
	apperrors "github.com/ppablomunoz/coda/internal/errors"
	"github.com/ppablomunoz/coda/internal/store"
	"github.com/ppablomunoz/coda/internal/utils"
)

func TestEnqueueEmbedReleaseCoverDedup(t *testing.T) {
	q := NewQueue(nil)
	q.EnqueueEmbedReleaseCover("rel-1")
	q.EnqueueEmbedReleaseCover("rel-1")
	q.EnqueueEmbedReleaseCover("rel-2")

	q.mu.Lock()
	defer q.mu.Unlock()
	n := 0
	for _, item := range q.items {
		if item.Key == TaskEmbedReleaseCover && item.ID == "rel-1" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("embed tasks for rel-1 = %d, want 1 (dedup)", n)
	}
	if len(q.items) != 2 {
		t.Errorf("queued = %d, want 2 (rel-1 + rel-2)", len(q.items))
	}
}

// tinyPNG is a valid 1x1 PNG for embed round-trips.
var embedPNG = mustDecodeB64Embed("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==")

func mustDecodeB64Embed(s string) []byte {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

func writeTestWav(t *testing.T, path string) {
	t.Helper()

	const sampleRate = 44100
	const samples = sampleRate / 10 // 100ms of silence
	data := make([]byte, samples*2)

	var buf bytes.Buffer
	buf.WriteString("RIFF")
	sizes := make([]byte, 4)
	binary.LittleEndian.PutUint32(sizes, uint32(36+len(data)))
	buf.Write(sizes)
	buf.WriteString("WAVE")

	buf.WriteString("fmt ")
	binary.LittleEndian.PutUint32(sizes, 16)
	buf.Write(sizes)
	meta := make([]byte, 16)
	binary.LittleEndian.PutUint16(meta[0:], 1)
	binary.LittleEndian.PutUint16(meta[2:], 1)
	binary.LittleEndian.PutUint32(meta[4:], sampleRate)
	binary.LittleEndian.PutUint32(meta[8:], sampleRate*2)
	binary.LittleEndian.PutUint16(meta[12:], 2)
	binary.LittleEndian.PutUint16(meta[14:], 16)
	buf.Write(meta)

	buf.WriteString("data")
	binary.LittleEndian.PutUint32(sizes, uint32(len(data)))
	buf.Write(sizes)
	buf.Write(data)

	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write wav: %v", err)
	}
}

const (
	embedArtistID = "d5b32d03-2aaf-4baf-ba5d-3721d095b833"
	embedGroupID  = "11111111-2222-4333-8444-555555555555"
	embedRelID    = "33333333-4444-4555-b666-777777777777"
	embedTrackID  = "55555555-6666-4777-b888-999999999999"
)

func newEmbedRunner(t *testing.T, root string) (*Runner, *store.Store) {
	t.Helper()
	log := utils.NewWithWriter(io.Discard)
	st, err := store.NewStore(filepath.Join(t.TempDir(), "test.db"), []string{root}, log)
	if err != nil {
		t.Fatalf("NewStore = %v", err)
	}
	t.Cleanup(st.Close)
	if err := st.AddRootDirs(context.Background()); err != nil {
		t.Fatalf("AddRootDirs = %v", err)
	}
	q := NewQueue(log)
	coverDir := t.TempDir()
	r := NewRunner(st, nil, log, q, nil, coverDir)
	return r, st
}

func seedEmbedRelease(t *testing.T, st *store.Store) {
	t.Helper()
	if err := st.CreateArtist(context.Background(), store.Artist{ID: embedArtistID, Name: "Embed Artist"}); err != nil {
		t.Fatalf("CreateArtist = %v", err)
	}
	credit := []store.CreditEntry{{ArtistID: embedArtistID, Name: "Embed Artist", CreditedName: "Embed Artist"}}
	if err := st.SaveDiscography(context.Background(), []store.FullReleaseParams{{
		ID: embedRelID, Title: "Embed Release", Credit: credit,
		Group: store.ReleaseGroupParams{ID: embedGroupID, Title: "Embed Group", Type: "album", Credit: credit},
		Tracks: []store.TrackParams{{
			ID: embedTrackID, ReleaseID: embedRelID,
			Disc: 1, Position: 1, Number: "1", Title: "Embed Track",
			Credit:      credit,
			RecordingID: "77777777-8888-4999-baaa-bbbbbbbbbbbb",
		}},
	}}); err != nil {
		t.Fatalf("SaveDiscography = %v", err)
	}
}

func syncAndLinkWav(t *testing.T, st *store.Store, root, name string) string {
	t.Helper()
	path := filepath.Join(root, name)
	writeTestWav(t, path)
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat = %v", err)
	}
	if _, err := st.SyncLocalFiles(context.Background(), []store.ScannedFile{{
		LibraryRoot: root, Path: path,
		SizeBytes: fi.Size(), MtimeNs: fi.ModTime().UnixNano(), FormatHint: "wav",
	}}, "2024-01-01 00:00:00", store.SyncOptions{}); err != nil {
		t.Fatalf("SyncLocalFiles = %v", err)
	}
	files, _, err := st.ListFiles(context.Background(), store.FileListOptions{PageOptions: store.PageOptions{Limit: 100}})
	if err != nil {
		t.Fatalf("ListFiles = %v", err)
	}
	var id int64
	for _, f := range files {
		if f.Path == path {
			id = f.ID
		}
	}
	if id == 0 {
		t.Fatalf("synced file %q not found", path)
	}
	if err := st.Link(context.Background(), id, embedTrackID, path, fi.Size(), fi.ModTime().UnixNano()); err != nil {
		t.Fatalf("Link = %v", err)
	}
	return path
}

func TestEmbedReleaseCoverRoundTrip(t *testing.T) {
	root := t.TempDir()
	r, st := newEmbedRunner(t, root)
	seedEmbedRelease(t, st)
	path := syncAndLinkWav(t, st, root, "song.wav")

	if err := SaveCover(context.Background(), st, r.coverDir, embedRelID, &artwork.Result{
		Bytes: embedPNG, MIME: "image/png", Source: artwork.SourceUpload,
	}); err != nil {
		t.Fatalf("SaveCover = %v", err)
	}

	r.embedReleaseCover(context.Background(), embedRelID)

	got, err := taglib.ReadImage(path)
	if err != nil {
		t.Fatalf("ReadImage = %v", err)
	}
	if !bytes.Equal(got, embedPNG) {
		t.Errorf("embedded bytes differ (%d vs %d bytes)", len(got), len(embedPNG))
	}
}

func TestEmbedReleaseCoverNoCoverNoop(t *testing.T) {
	root := t.TempDir()
	r, st := newEmbedRunner(t, root)
	seedEmbedRelease(t, st)
	syncAndLinkWav(t, st, root, "song.wav")

	// No cover row: must not fail and must not touch the file.
	r.embedReleaseCover(context.Background(), embedRelID)

	files, err := st.ListFilesByRelease(context.Background(), embedRelID)
	if err != nil {
		t.Fatalf("ListFilesByRelease = %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("files = %d, want 1", len(files))
	}
}

func TestEmbedReleaseCoverStaleRowDropped(t *testing.T) {
	root := t.TempDir()
	r, st := newEmbedRunner(t, root)
	seedEmbedRelease(t, st)

	// Row without bytes: the run drops the stale row so the next GET
	// refetches instead of 500ing forever.
	if err := st.UpsertCover(context.Background(), store.CoverParams{
		ReleaseID: embedRelID, Source: artwork.SourceUpload, MIME: "image/png", SizeBytes: 10,
	}); err != nil {
		t.Fatalf("UpsertCover = %v", err)
	}
	r.embedReleaseCover(context.Background(), embedRelID)

	if _, err := st.GetCover(context.Background(), embedRelID); err == nil {
		t.Error("stale row still present, want it dropped")
	}
}

func TestExecEmbedReleaseCoverNoPanic(t *testing.T) {
	r, _, _ := newTestRunner(t)
	r.exec(context.Background(), Task{Key: TaskEmbedReleaseCover, ID: embedRelID})
}

func TestEnqueueClearReleaseCoverDedup(t *testing.T) {
	q := NewQueue(nil)
	q.EnqueueClearReleaseCover("rel-1")
	q.EnqueueClearReleaseCover("rel-1")
	q.EnqueueClearReleaseCover("rel-2")

	q.mu.Lock()
	defer q.mu.Unlock()
	n := 0
	for _, item := range q.items {
		if item.Key == TaskClearReleaseCover && item.ID == "rel-1" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("clear tasks for rel-1 = %d, want 1 (dedup)", n)
	}
	if len(q.items) != 2 {
		t.Errorf("queued = %d, want 2 (rel-1 + rel-2)", len(q.items))
	}
}

func TestEnsureCoverRemovedNeverFetches(t *testing.T) {
	root := t.TempDir()
	r, st := newEmbedRunner(t, root)
	seedEmbedRelease(t, st)
	if err := SaveCover(context.Background(), st, r.coverDir, embedRelID, &artwork.Result{
		Bytes: embedPNG, MIME: "image/png", Source: artwork.SourceUpload,
	}); err != nil {
		t.Fatalf("SaveCover = %v", err)
	}
	if err := st.MarkCoverRemoved(context.Background(), embedRelID); err != nil {
		t.Fatalf("MarkCoverRemoved = %v", err)
	}

	// The cached row exists but must not be served, and no fetch happens
	// (nil fetcher would answer NotFound for a plain miss too — the point
	// is the live row is suppressed).
	if _, err := EnsureCover(context.Background(), st, nil, r.coverDir, embedRelID, "", ""); !apperrors.Is(err, apperrors.ErrNotFound) {
		t.Errorf("EnsureCover removed = %v, want NOT_FOUND", err)
	}
}

func TestEmbedCoverLeavesTombstone(t *testing.T) {
	root := t.TempDir()
	r, st := newEmbedRunner(t, root)
	seedEmbedRelease(t, st)
	path := syncAndLinkWav(t, st, root, "song.wav")
	if err := st.MarkCoverRemoved(context.Background(), embedRelID); err != nil {
		t.Fatalf("MarkCoverRemoved = %v", err)
	}

	if err := EmbedCover(context.Background(), st, r.coverDir, path, embedRelID); err != nil {
		t.Fatalf("EmbedCover removed = %v, want nil no-op", err)
	}
	row, err := st.GetCover(context.Background(), embedRelID)
	if err != nil {
		t.Fatalf("GetCover = %v", err)
	}
	if !row.Removed {
		t.Error("tombstone dropped by EmbedCover, want it kept")
	}
	// embedReleaseCover also no-ops on the tombstone.
	r.embedReleaseCover(context.Background(), embedRelID)
	if row, err := st.GetCover(context.Background(), embedRelID); err != nil || !row.Removed {
		t.Errorf("tombstone after embed task = %+v, %v; want kept", row, err)
	}
}

func TestClearReleaseCoverRoundTrip(t *testing.T) {
	root := t.TempDir()
	r, st := newEmbedRunner(t, root)
	seedEmbedRelease(t, st)
	path := syncAndLinkWav(t, st, root, "song.wav")
	if err := SaveCover(context.Background(), st, r.coverDir, embedRelID, &artwork.Result{
		Bytes: embedPNG, MIME: "image/png", Source: artwork.SourceUpload,
	}); err != nil {
		t.Fatalf("SaveCover = %v", err)
	}
	r.embedReleaseCover(context.Background(), embedRelID)
	if got, err := taglib.ReadImage(path); err != nil || !bytes.Equal(got, embedPNG) {
		t.Fatalf("embedded setup = %d bytes, %v; want uploaded art", len(got), err)
	}

	if err := st.MarkCoverRemoved(context.Background(), embedRelID); err != nil {
		t.Fatalf("MarkCoverRemoved = %v", err)
	}
	r.clearReleaseCover(context.Background(), embedRelID)

	if got, err := taglib.ReadImage(path); err != nil {
		t.Fatalf("ReadImage = %v", err)
	} else if len(got) != 0 {
		t.Errorf("embedded bytes after clear = %d, want 0", len(got))
	}
	// The tombstone survives the file wipe so GETs stay 404.
	if row, err := st.GetCover(context.Background(), embedRelID); err != nil || !row.Removed {
		t.Errorf("tombstone after clear = %+v, %v; want kept", row, err)
	}
}

func TestClearReleaseCoverSkipsLiveCover(t *testing.T) {
	root := t.TempDir()
	r, st := newEmbedRunner(t, root)
	seedEmbedRelease(t, st)
	path := syncAndLinkWav(t, st, root, "song.wav")
	if err := SaveCover(context.Background(), st, r.coverDir, embedRelID, &artwork.Result{
		Bytes: embedPNG, MIME: "image/png", Source: artwork.SourceUpload,
	}); err != nil {
		t.Fatalf("SaveCover = %v", err)
	}
	r.embedReleaseCover(context.Background(), embedRelID)

	// A removal followed by a new upload clears the tombstone; the queued
	// clear must not wipe the fresh art.
	if err := st.MarkCoverRemoved(context.Background(), embedRelID); err != nil {
		t.Fatalf("MarkCoverRemoved = %v", err)
	}
	if err := SaveCover(context.Background(), st, r.coverDir, embedRelID, &artwork.Result{
		Bytes: embedPNG, MIME: "image/png", Source: artwork.SourceUpload,
	}); err != nil {
		t.Fatalf("re-SaveCover = %v", err)
	}
	r.clearReleaseCover(context.Background(), embedRelID)

	if got, err := taglib.ReadImage(path); err != nil || !bytes.Equal(got, embedPNG) {
		t.Errorf("embedded bytes after skipped clear differ, err=%v", err)
	}
}

func TestExecClearReleaseCoverNoPanic(t *testing.T) {
	r, _, _ := newTestRunner(t)
	r.exec(context.Background(), Task{Key: TaskClearReleaseCover, ID: embedRelID})
}
