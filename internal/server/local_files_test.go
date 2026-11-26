package server

import (
	"bytes"
	"context"
	"encoding/binary"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/ppablomunoz/coda/internal/store"
)

const (
	linkTestArtistID = "d5b32d03-2aaf-4baf-ba5d-3721d095b833"
	linkTestGroupID  = "11111111-2222-4333-8444-555555555555"
	linkTestRelID    = "33333333-4444-4555-b666-777777777777"
	linkTestTrackID  = "55555555-6666-4777-b888-999999999999"
)

func writeLinkWav(t *testing.T, path string) {
	t.Helper()

	const sampleRate = 44100
	const samples = sampleRate / 10
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

func seedLinkRelease(t *testing.T, st *store.Store) {
	t.Helper()
	ctx := context.Background()
	if err := st.CreateArtist(ctx, store.Artist{ID: linkTestArtistID, Name: "Link Artist"}); err != nil {
		t.Fatalf("CreateArtist = %v", err)
	}
	credit := []store.CreditEntry{{ArtistID: linkTestArtistID, Name: "Link Artist", CreditedName: "Link Artist"}}
	if err := st.SaveDiscography(ctx, []store.FullReleaseParams{{
		ID: linkTestRelID, Title: "Link Release", Credit: credit,
		Group: store.ReleaseGroupParams{ID: linkTestGroupID, Title: "Link Group", Type: "album", Credit: credit},
		Tracks: []store.TrackParams{{
			ID: linkTestTrackID, ReleaseID: linkTestRelID,
			Disc: 1, Position: 1, Number: "1", Title: "Link Track",
			Credit:      credit,
			RecordingID: "77777777-8888-4999-baaa-bbbbbbbbbbbb",
		}},
	}}); err != nil {
		t.Fatalf("SaveDiscography = %v", err)
	}
}

func syncLinkWav(t *testing.T, st *store.Store, root, name string) (int64, string) {
	t.Helper()
	path := filepath.Join(root, name)
	writeLinkWav(t, path)
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat = %v", err)
	}
	if _, err := st.SyncLocalFiles(context.Background(), []store.ScannedFile{{
		LibraryRoot: root, Path: path,
		SizeBytes: fi.Size(), MtimeNs: fi.ModTime().UnixNano(), FormatHint: "wav",
	}}, "2024-01-01 00:00:00"); err != nil {
		t.Fatalf("SyncLocalFiles = %v", err)
	}
	files, _, err := st.ListFiles(context.Background(), store.FileListOptions{PageOptions: store.PageOptions{Limit: 100}})
	if err != nil {
		t.Fatalf("ListFiles = %v", err)
	}
	for _, f := range files {
		if f.Path == path {
			return f.ID, f.Path
		}
	}
	t.Fatalf("synced file %q not found", path)
	return 0, ""
}

// TestLinkConcurrentSameDestination fires two link requests for different
// files at the same track concurrently: exactly one must win with 204, the
// other must fail with 409, and no file may be lost.
func TestLinkConcurrentSameDestination(t *testing.T) {
	root := t.TempDir()
	env := newTestEnv(t, root)
	seedLinkRelease(t, env.st)

	idA, pathA := syncLinkWav(t, env.st, root, "a.wav")
	idB, pathB := syncLinkWav(t, env.st, root, "b.wav")

	type outcome struct{ code int }
	outcomes := make([]outcome, 2)
	var wg sync.WaitGroup
	for i, id := range []int64{idA, idB} {
		wg.Add(1)
		go func(i int, id int64) {
			defer wg.Done()
			w := doRequest(t, env.router, "POST",
				"/api/v1/files/"+itoa(id)+"/link",
				map[string]string{"track_id": linkTestTrackID})
			outcomes[i] = outcome{w.Code}
		}(i, id)
	}
	wg.Wait()

	var ok204, conflict409 int
	for _, o := range outcomes {
		switch o.code {
		case http.StatusNoContent:
			ok204++
		case http.StatusConflict:
			conflict409++
		default:
			t.Errorf("link status = %d, want 204 or 409", o.code)
		}
	}
	if ok204 != 1 || conflict409 != 1 {
		t.Fatalf("204=%d 409=%d, want exactly one of each", ok204, conflict409)
	}

	// No lost file: the winner moved to its canonical path, the loser is
	// still at its source path with its DB row unlinked.
	rowA, err := env.st.GetFile(context.Background(), idA, false)
	if err != nil {
		t.Fatal(err)
	}
	rowB, err := env.st.GetFile(context.Background(), idB, false)
	if err != nil {
		t.Fatal(err)
	}
	var winner, loser *store.LocalFile
	var loserSrc string
	switch {
	case rowA.TrackID != nil && rowB.TrackID == nil:
		winner, loser, loserSrc = rowA, rowB, pathB
	case rowB.TrackID != nil && rowA.TrackID == nil:
		winner, loser, loserSrc = rowB, rowA, pathA
	default:
		t.Fatalf("linked states A=%v B=%v, want exactly one linked", rowA.TrackID, rowB.TrackID)
	}
	if _, err := os.Stat(winner.Path); err != nil {
		t.Errorf("winner file missing at %q: %v", winner.Path, err)
	}
	if loser.Path != loserSrc {
		t.Errorf("loser path = %q, want untouched %q", loser.Path, loserSrc)
	}
	if _, err := os.Stat(loserSrc); err != nil {
		t.Errorf("loser file missing at %q: %v", loserSrc, err)
	}
}

// TestLinkMissingEntitiesAre404 guards the shared pipeline's error mapping:
// unknown files and tracks answer 404, never 500.
func TestLinkMissingEntitiesAre404(t *testing.T) {
	root := t.TempDir()
	env := newTestEnv(t, root)
	seedLinkRelease(t, env.st)
	id, _ := syncLinkWav(t, env.st, root, "a.wav")

	w := doRequest(t, env.router, "POST", "/api/v1/files/999999/link",
		map[string]string{"track_id": linkTestTrackID})
	if w.Code != http.StatusNotFound {
		t.Errorf("missing file = %d, want 404", w.Code)
	}

	w = doRequest(t, env.router, "POST",
		"/api/v1/files/"+itoa(id)+"/link",
		map[string]string{"track_id": "00000000-0000-4000-8000-000000000000"})
	if w.Code != http.StatusNotFound {
		t.Errorf("missing track = %d, want 404", w.Code)
	}
}

func itoa(id int64) string {
	if id == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	neg := id < 0
	if neg {
		id = -id
	}
	for id > 0 {
		i--
		buf[i] = byte('0' + id%10)
		id /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
