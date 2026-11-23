package library

import (
	"context"
	"os"
	"path/filepath"
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
	if err := Scan(context.Background(), roots, func(f File) error {
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

func TestScanEmptyRoots(t *testing.T) {
	called := false
	if err := Scan(context.Background(), nil, func(File) error {
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
	err := Scan(ctx, []string{root}, func(File) error { return nil })
	if err == nil {
		t.Error("Scan(cancelled) = nil, want context error")
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
