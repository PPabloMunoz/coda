package tags

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
)

// writeWav creates a minimal valid 16-bit PCM WAV file so taglib has a
// real audio container to write tags into.
func writeWav(t *testing.T, path string) {
	t.Helper()

	const sampleRate = 44100
	const samples = sampleRate / 10 // 100ms of silence
	data := make([]byte, samples*2) // mono, 16-bit

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
	binary.LittleEndian.PutUint16(meta[0:], 1)            // PCM
	binary.LittleEndian.PutUint16(meta[2:], 1)            // mono
	binary.LittleEndian.PutUint32(meta[4:], sampleRate)   // Hz
	binary.LittleEndian.PutUint32(meta[8:], sampleRate*2) // byte rate
	binary.LittleEndian.PutUint16(meta[12:], 2)           // block align
	binary.LittleEndian.PutUint16(meta[14:], 16)          // bits
	buf.Write(meta)

	buf.WriteString("data")
	binary.LittleEndian.PutUint32(sizes, uint32(len(data)))
	buf.Write(sizes)
	buf.Write(data)

	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write wav: %v", err)
	}
}

func tempFilesLeft(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	var left []string
	for _, e := range entries {
		if name := e.Name(); len(name) > 10 && name[:10] == ".coda-tag-" {
			left = append(left, name)
		}
	}
	return left
}

func TestMutateReplacesFileOnSuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "song.wav")
	writeWav(t, path)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}

	err = mutate(path, func(tmp string) error {
		return os.WriteFile(tmp, []byte("rewritten"), 0o0400)
	})
	if err != nil {
		t.Fatalf("mutate: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "rewritten" {
		t.Errorf("content = %q, want rewritten", got)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if after.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want -rw------- (original preserved)", after.Mode())
	}
	_ = before
	if left := tempFilesLeft(t, dir); len(left) > 0 {
		t.Errorf("temp files left behind: %v", left)
	}
}

func TestMutateKeepsOriginalOnFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "song.wav")
	writeWav(t, path)
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	wantErr := fmt.Errorf("boom")
	if err := mutate(path, func(tmp string) error { return wantErr }); err == nil {
		t.Fatal("mutate = nil error, want the write failure")
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, original) {
		t.Error("original file was modified by a failed mutate")
	}
	if left := tempFilesLeft(t, dir); len(left) > 0 {
		t.Errorf("temp files left behind: %v", left)
	}
}

func TestMutateSerializesSamePath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "counter")
	if err := os.WriteFile(path, []byte("0"), 0o644); err != nil {
		t.Fatal(err)
	}

	const goroutines, perGoroutine = 8, 25
	var wg sync.WaitGroup
	errs := make(chan error, goroutines*perGoroutine)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				err := mutate(path, func(tmp string) error {
					raw, err := os.ReadFile(tmp)
					if err != nil {
						return err
					}
					n, err := strconv.Atoi(string(bytes.TrimSpace(raw)))
					if err != nil {
						return err
					}
					return os.WriteFile(tmp, []byte(strconv.Itoa(n+1)), 0o644)
				})
				if err != nil {
					errs <- err
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("mutate: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(raw), strconv.Itoa(goroutines*perGoroutine); got != want {
		t.Errorf("counter = %s, want %s (lost updates means the per-path lock leaked)", got, want)
	}
}

func TestWriteTagsRoundTripWAV(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "song.wav")
	writeWav(t, path)

	snap := &Snapshot{Title: "Song", Album: "Record", ArtistRaw: "Alice", TrackNumber: 3}
	if err := WriteTags(path, ToMap(snap)); err != nil {
		t.Fatalf("WriteTags: %v", err)
	}
	back, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if back.Title != "Song" || back.Album != "Record" || back.TrackNumber != 3 {
		t.Errorf("after write = %+v", back)
	}
	if left := tempFilesLeft(t, dir); len(left) > 0 {
		t.Errorf("temp files left behind: %v", left)
	}

	if err := ClearTags(path); err != nil {
		t.Fatalf("ClearTags: %v", err)
	}
	back, err = Read(path)
	if err != nil {
		t.Fatalf("Read after clear: %v", err)
	}
	if back.Title != "" || back.TrackNumber != 0 {
		t.Errorf("after clear = %+v, want empty link keys", back)
	}
	if left := tempFilesLeft(t, dir); len(left) > 0 {
		t.Errorf("temp files left behind: %v", left)
	}
}

func TestTempPatternKeepsExtension(t *testing.T) {
	cases := map[string]string{
		"/a/b/song.mp3":  ".coda-tag-*.mp3",
		"/a/b/song.flac": ".coda-tag-*.flac",
		"/a/b/noext":     ".coda-tag-",
	}
	for path, want := range cases {
		if got := tempPattern(path); got != want {
			t.Errorf("tempPattern(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestIsTempFile(t *testing.T) {
	cases := map[string]bool{
		".coda-tag-12345.mp3":     true,
		"/a/b/.coda-tag-xyz.wav":  true,
		"/a/b/.coda-tag-":         true,
		"song.mp3":                false,
		".hidden.mp3":             false,
		"/a/b/.coda-cover-x.tmp":  false,
		"/a/b/coda-tag-12345.mp3": false,
		"":                        false,
	}
	for name, want := range cases {
		if got := IsTempFile(name); got != want {
			t.Errorf("IsTempFile(%q) = %t, want %t", name, got, want)
		}
	}
}
