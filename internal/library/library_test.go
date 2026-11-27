package library

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("audio"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func collectScan(t *testing.T, roots []string) []File {
	t.Helper()
	var got []File
	if _, err := Scan(context.Background(), roots, func(f File) error {
		got = append(got, f)
		return nil
	}); err != nil {
		t.Fatalf("Scan = %v, want nil", err)
	}
	return got
}

func TestScanFindsSupportedFormats(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "song.mp3"))
	writeFile(t, filepath.Join(root, "song.FLAC"))
	writeFile(t, filepath.Join(root, "notes.txt"))
	writeFile(t, filepath.Join(root, "sub", "track.ogg"))

	got := collectScan(t, []string{root})
	if len(got) != 3 {
		t.Fatalf("found %d files, want 3: %v", len(got), got)
	}
	for _, f := range got {
		if f.LibraryRoot != root {
			t.Errorf("LibraryRoot = %q, want %q", f.LibraryRoot, root)
		}
		if f.SizeBytes == 0 {
			t.Errorf("SizeBytes = 0 for %q", f.Path)
		}
		if f.FormatHint == "" {
			t.Errorf("FormatHint empty for %q", f.Path)
		}
	}
}

func TestScanSkipsSymlinks(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real.mp3")
	writeFile(t, real)
	link := filepath.Join(root, "link.mp3")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	got := collectScan(t, []string{root})
	if len(got) != 1 {
		t.Fatalf("found %d files, want 1 (symlink skipped): %v", len(got), got)
	}
	if got[0].Path != real {
		t.Errorf("Path = %q, want %q", got[0].Path, real)
	}
}

func TestScanSkipsHiddenFilesAndDirs(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "song.mp3"))
	writeFile(t, filepath.Join(root, ".hidden.mp3"))
	writeFile(t, filepath.Join(root, ".coda-tag-abc123.mp3"))
	writeFile(t, filepath.Join(root, ".hidden-dir", "inner.mp3"))
	writeFile(t, filepath.Join(root, "sub", "track.ogg"))
	writeFile(t, filepath.Join(root, "sub", ".coda-tag-xyz.wav"))

	got := collectScan(t, []string{root})
	if len(got) != 2 {
		t.Fatalf("found %d files, want 2 (hidden skipped): %v", len(got), got)
	}
	for _, f := range got {
		if strings.HasPrefix(filepath.Base(f.Path), ".") {
			t.Errorf("hidden file indexed: %q", f.Path)
		}
		if strings.Contains(f.Path, ".hidden-dir") {
			t.Errorf("file under hidden dir indexed: %q", f.Path)
		}
	}
}

// A root whose own name starts with "." is still scanned; only entries
// below the root are subject to the hidden skip.
func TestScanDotRootStillScanned(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".library")
	writeFile(t, filepath.Join(root, "song.mp3"))
	writeFile(t, filepath.Join(root, ".coda-tag-abc.mp3"))

	got := collectScan(t, []string{root})
	if len(got) != 1 {
		t.Fatalf("found %d files, want 1 (dot root scanned, temp skipped): %v", len(got), got)
	}
	if want := filepath.Join(root, "song.mp3"); got[0].Path != want {
		t.Errorf("Path = %q, want %q", got[0].Path, want)
	}
}

func TestScanEmptyRoots(t *testing.T) {
	called := false
	if _, err := Scan(context.Background(), nil, func(File) error {
		called = true
		return nil
	}); err != nil {
		t.Fatalf("Scan(nil) = %v, want nil", err)
	}
	if called {
		t.Error("callback called with no roots, want no calls")
	}
}

func TestScanContextCancel(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.mp3"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Scan(ctx, []string{root}, func(File) error { return nil })
	if err == nil {
		t.Error("Scan(cancelled) = nil, want context error")
	}
}

func TestScanReportsCompleteRoot(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.mp3"))
	writeFile(t, filepath.Join(root, "sub", "b.flac"))

	statuses, err := Scan(context.Background(), []string{root}, func(File) error { return nil })
	if err != nil {
		t.Fatalf("Scan = %v", err)
	}
	if len(statuses) != 1 {
		t.Fatalf("statuses = %v, want 1 entry", statuses)
	}
	if !statuses[0].Walked {
		t.Errorf("Walked = false, want true")
	}
	if statuses[0].WalkErr != nil {
		t.Errorf("WalkErr = %v, want nil", statuses[0].WalkErr)
	}
	if statuses[0].Files != 2 {
		t.Errorf("Files = %d, want 2", statuses[0].Files)
	}
}

func TestScanMissingRootIncomplete(t *testing.T) {
	good := t.TempDir()
	writeFile(t, filepath.Join(good, "a.mp3"))
	missing := filepath.Join(t.TempDir(), "does-not-exist")

	var got []File
	statuses, err := Scan(context.Background(), []string{missing, good}, func(f File) error {
		got = append(got, f)
		return nil
	})
	if err != nil {
		t.Fatalf("Scan = %v, want nil (missing root must not abort)", err)
	}
	if len(statuses) != 2 {
		t.Fatalf("statuses = %v, want 2 entries", statuses)
	}
	if statuses[0].Walked {
		t.Error("missing root Walked = true, want false")
	}
	if statuses[0].WalkErr == nil {
		t.Error("missing root WalkErr = nil, want an error")
	}
	if statuses[0].Files != 0 {
		t.Errorf("missing root Files = %d, want 0", statuses[0].Files)
	}
	if !statuses[1].Walked || statuses[1].Files != 1 {
		t.Errorf("good root status = %+v, want Walked with 1 file", statuses[1])
	}
	if len(got) != 1 {
		t.Errorf("files = %d, want 1 (good root still scanned)", len(got))
	}
}

func TestScanUnreadableSubdirIncomplete(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "top.mp3"))
	sub := filepath.Join(root, "locked")
	writeFile(t, filepath.Join(sub, "hidden.mp3"))
	if err := os.Chmod(sub, 0o000); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(sub, 0o755) }()

	// Root running with elevated privileges can still read through
	// 0o000; skip rather than assert a platform-dependent failure.
	if _, err := os.ReadDir(sub); err == nil {
		t.Skip("unreadable dir still readable (likely running as root), skipping")
	}

	statuses, err := Scan(context.Background(), []string{root}, func(File) error { return nil })
	if err != nil {
		t.Fatalf("Scan = %v, want nil", err)
	}
	if len(statuses) != 1 {
		t.Fatalf("statuses = %v, want 1 entry", statuses)
	}
	if statuses[0].Walked {
		t.Error("Walked = true, want false for unreadable subdir")
	}
	if statuses[0].WalkErr == nil {
		t.Error("WalkErr = nil, want an error for unreadable subdir")
	}
}

func TestTargetPath(t *testing.T) {
	root := t.TempDir()

	got, err := Target{Root: root, Artist: "The Beatles", Album: "Abbey Road", Title: "Come Together", Disc: 1, Track: 1, Ext: "mp3"}.Path()
	if err != nil {
		t.Fatalf("Path() = %v, want nil", err)
	}
	want := filepath.Join(root, "The_Beatles", "Abbey_Road", "01_Come_Together.mp3")
	if got != want {
		t.Errorf("Path() = %q, want %q", got, want)
	}
}

func TestTargetPathMultiDisc(t *testing.T) {
	root := t.TempDir()

	got, err := Target{Root: root, Artist: "The Beatles", Album: "Anthology", Title: "Come Together", Disc: 2, Track: 1, TotalDiscs: 3, Ext: "mp3"}.Path()
	if err != nil {
		t.Fatalf("Path() = %v, want nil", err)
	}
	want := filepath.Join(root, "The_Beatles", "Anthology", "02-01_Come_Together.mp3")
	if got != want {
		t.Errorf("Path() = %q, want %q", got, want)
	}

	// Disc 1 of a multi-disc release keeps the prefix to avoid collisions
	// across discs in the same album folder.
	got, err = Target{Root: root, Artist: "A", Album: "B", Title: "C", Disc: 1, Track: 2, TotalDiscs: 2, Ext: "mp3"}.Path()
	if err != nil {
		t.Fatalf("Path() = %v, want nil", err)
	}
	want = filepath.Join(root, "A", "B", "01-02_C.mp3")
	if got != want {
		t.Errorf("Path() = %q, want %q", got, want)
	}
}

func TestTargetPathErrors(t *testing.T) {
	root := t.TempDir()
	base := Target{Root: root, Artist: "A", Album: "B", Title: "C", Disc: 1, Track: 1, Ext: "mp3"}

	cases := []struct {
		name   string
		mutate func(*Target)
	}{
		{"empty root", func(t *Target) { t.Root = "" }},
		{"relative root", func(t *Target) { t.Root = "relative" }},
		{"empty artist", func(t *Target) { t.Artist = "   " }},
		{"empty album", func(t *Target) { t.Album = "" }},
		{"empty title", func(t *Target) { t.Title = "" }},
		{"disc zero", func(t *Target) { t.Disc = 0 }},
		{"track zero", func(t *Target) { t.Track = 0 }},
		{"empty ext", func(t *Target) { t.Ext = "" }},
		{"unsupported ext", func(t *Target) { t.Ext = "txt" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tg := base
			tc.mutate(&tg)
			if _, err := tg.Path(); err == nil {
				t.Errorf("Path() = nil, want error for %s", tc.name)
			}
		})
	}
}

func TestTargetPathExtensionNormalized(t *testing.T) {
	root := t.TempDir()
	got, err := Target{Root: root, Artist: "A", Album: "B", Title: "C", Disc: 1, Track: 2, Ext: ".MP3"}.Path()
	if err != nil {
		t.Fatalf("Path() = %v, want nil", err)
	}
	want := filepath.Join(root, "A", "B", "02_C.mp3")
	if got != want {
		t.Errorf("Path() = %q, want %q", got, want)
	}
}
