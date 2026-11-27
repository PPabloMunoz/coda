package server

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ppablomunoz/coda/internal/store"
	"github.com/ppablomunoz/coda/internal/utils"
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
	}}, time.Now().UTC().Format(time.RFC3339), store.SyncOptions{}); err != nil {
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

// assertAPIError checks the HTTP status and the JSON "code" field of an
// error response.
func assertAPIError(t *testing.T, w *httptest.ResponseRecorder, wantStatus int, wantCode string) {
	t.Helper()
	if w.Code != wantStatus {
		t.Errorf("status = %d, want %d (body %q)", w.Code, wantStatus, w.Body.String())
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Errorf("body is not a JSON error object: %v (body %q)", err, w.Body.String())
		return
	}
	if body.Code != wantCode {
		t.Errorf("code = %q, want %q (body %q)", body.Code, wantCode, w.Body.String())
	}
}

// deactivateAllRoots soft-deactivates every root in env's database by opening
// the same DB with an empty root set, mirroring a root removed from config.
func deactivateAllRoots(t *testing.T, env *testEnv) {
	t.Helper()
	st2, err := store.NewStore(env.dbPath, nil, utils.NewWithWriter(io.Discard))
	if err != nil {
		t.Fatalf("NewStore = %v", err)
	}
	t.Cleanup(st2.Close)
	if err := st2.AddRootDirs(context.Background()); err != nil {
		t.Fatalf("AddRootDirs = %v", err)
	}
}

// TestFileActionsMissingEntitiesAre404 is the table-driven guard for issue
// #37: every file action on a missing file, and linking to an unknown
// track, must answer 404 NOT_FOUND, never 500.
func TestFileActionsMissingEntitiesAre404(t *testing.T) {
	root := t.TempDir()
	env := newTestEnv(t, root)
	seedLinkRelease(t, env.st)
	id, _ := syncLinkWav(t, env.st, root, "a.wav")

	const unknownTrackID = "00000000-0000-4000-8000-000000000000"

	cases := []struct {
		name   string
		method string
		path   string
		body   any
	}{
		{"get file", "GET", "/api/v1/files/999999", nil},
		{"suggestions", "GET", "/api/v1/files/999999/suggestions", nil},
		{"link", "POST", "/api/v1/files/999999/link", map[string]string{"track_id": linkTestTrackID}},
		{"unlink", "DELETE", "/api/v1/files/999999/link", nil},
		{"refresh", "POST", "/api/v1/files/999999/refresh", nil},
		{"link unknown track", "POST", "/api/v1/files/" + itoa(id) + "/link", map[string]string{"track_id": unknownTrackID}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := doRequest(t, env.router, tc.method, tc.path, tc.body)
			assertAPIError(t, w, http.StatusNotFound, "NOT_FOUND")
		})
	}
}

// TestFileActionsInactiveRootAre404 removes the root from config (rows and
// links are kept but hidden) and checks every file action answers 404
// NOT_FOUND for the now-hidden file.
func TestFileActionsInactiveRootAre404(t *testing.T) {
	root := t.TempDir()
	env := newTestEnv(t, root)
	seedLinkRelease(t, env.st)
	id, _ := syncLinkWav(t, env.st, root, "a.wav")
	deactivateAllRoots(t, env)

	cases := []struct {
		name   string
		method string
		path   string
		body   any
	}{
		{"get file", "GET", "/api/v1/files/" + itoa(id), nil},
		{"suggestions", "GET", "/api/v1/files/" + itoa(id) + "/suggestions", nil},
		{"link", "POST", "/api/v1/files/" + itoa(id) + "/link", map[string]string{"track_id": linkTestTrackID}},
		{"unlink", "DELETE", "/api/v1/files/" + itoa(id) + "/link", nil},
		{"refresh", "POST", "/api/v1/files/" + itoa(id) + "/refresh", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := doRequest(t, env.router, tc.method, tc.path, tc.body)
			assertAPIError(t, w, http.StatusNotFound, "NOT_FOUND")
		})
	}
}

// TestLinkOccupiedDestinationIs409 links one file to a track, then links a
// second file to the same track: the second call must fail 409
// ALREADY_EXISTS with the source file untouched.
func TestLinkOccupiedDestinationIs409(t *testing.T) {
	root := t.TempDir()
	env := newTestEnv(t, root)
	seedLinkRelease(t, env.st)
	idA, _ := syncLinkWav(t, env.st, root, "a.wav")
	idB, pathB := syncLinkWav(t, env.st, root, "b.wav")

	// Occupy the canonical destination with the first file.
	w := doRequest(t, env.router, "POST", "/api/v1/files/"+itoa(idA)+"/link",
		map[string]string{"track_id": linkTestTrackID})
	if w.Code != http.StatusNoContent {
		t.Fatalf("first link = %d, want 204", w.Code)
	}

	w = doRequest(t, env.router, "POST",
		"/api/v1/files/"+itoa(idB)+"/link",
		map[string]string{"track_id": linkTestTrackID})
	assertAPIError(t, w, http.StatusConflict, "ALREADY_EXISTS")

	// The loser is untouched at its source path with its DB row unlinked.
	rowB, err := env.st.GetFile(context.Background(), idB, false)
	if err != nil {
		t.Fatal(err)
	}
	if rowB.TrackID != nil {
		t.Errorf("loser TrackID = %v, want nil (unlinked)", *rowB.TrackID)
	}
	if rowB.Path != pathB {
		t.Errorf("loser path = %q, want untouched %q", rowB.Path, pathB)
	}
	if _, err := os.Stat(pathB); err != nil {
		t.Errorf("loser file missing at %q: %v", pathB, err)
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

// TestFileJSONTimestampsAreRFC3339 guards issue #48: scanned_at must parse
// as RFC3339 (Safari cannot parse the legacy space format) while mtime_ns
// stays a raw Unix-nanosecond integer for change detection.
func TestFileJSONTimestampsAreRFC3339(t *testing.T) {
	root := t.TempDir()
	env := newTestEnv(t, root)
	id, _ := syncLinkWav(t, env.st, root, "a.wav")

	w := doRequest(t, env.router, "GET", "/api/v1/files/"+itoa(id), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("get file = %d, want 200", w.Code)
	}
	var body struct {
		MtimeNs   int64  `json:"mtime_ns"`
		ScannedAt string `json:"scanned_at"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode file = %v", err)
	}
	if body.MtimeNs <= 0 {
		t.Errorf("mtime_ns = %d, want positive UnixNano", body.MtimeNs)
	}
	if _, err := time.Parse(time.RFC3339, body.ScannedAt); err != nil {
		t.Errorf("scanned_at = %q, want RFC3339: %v", body.ScannedAt, err)
	}
}
