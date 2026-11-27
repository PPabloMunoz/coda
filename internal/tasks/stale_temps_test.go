package tasks

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ppablomunoz/coda/internal/store"
	"github.com/ppablomunoz/coda/internal/tags"
)

// TestScanRootsIgnoresTaggedTempFile is the #38 regression: a leftover
// ".coda-tag-*" temp copy carrying a valid release-track MBID must never
// be indexed or auto-linked. The temp row seeded here simulates a version
// without the hidden-file skip; one scan must prune the bogus row while
// leaving the temp bytes on disk untouched and creating nothing at the
// canonical destination.
func TestScanRootsIgnoresTaggedTempFile(t *testing.T) {
	root := t.TempDir()
	r, st := newEmbedRunner(t, root)
	seedEmbedRelease(t, st)
	ctx := context.Background()

	normalPath := filepath.Join(root, "song.wav")
	writeTestWav(t, normalPath)

	tempPath := filepath.Join(root, ".coda-tag-abc123.wav")
	writeTestWav(t, tempPath)
	if err := tags.WriteTags(tempPath, tags.ToMap(&tags.Snapshot{
		Title: "Embed Track", Album: "Embed Release",
		ArtistRaw: "Embed Artist", TrackNumber: 1, DiscNumber: 1,
		MBReleaseTrackID: embedTrackID,
	})); err != nil {
		t.Fatalf("WriteTags temp = %v", err)
	}
	wantTemp, err := os.ReadFile(tempPath)
	if err != nil {
		t.Fatal(err)
	}

	var scanned []store.ScannedFile
	for _, p := range []string{normalPath, tempPath} {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		scanned = append(scanned, store.ScannedFile{
			LibraryRoot: root, Path: p,
			SizeBytes: fi.Size(), MtimeNs: fi.ModTime().UnixNano(), FormatHint: "wav",
		})
	}
	if _, err := st.SyncLocalFiles(ctx, scanned, "2024-01-01 00:00:00", store.SyncOptions{}); err != nil {
		t.Fatalf("seed SyncLocalFiles = %v", err)
	}

	// Fail fast if the fixture is broken: the seeded temp row must carry
	// the valid MBID as stored tag evidence (proving the tag round-tripped)
	// so the scan has something it could wrongly auto-link. Sync stores
	// evidence only; linking happens in the auto-link pass.
	candidates, err := st.ListAutoLinkCandidates(ctx, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var tempCandidate bool
	for _, c := range candidates {
		// Resolve the candidate path via ListFiles to compare.
		f, ferr := st.GetFile(ctx, c.ID, false)
		if ferr != nil {
			continue
		}
		if f.Path == tempPath && c.MbTrackID == embedTrackID {
			tempCandidate = true
		}
	}
	if !tempCandidate {
		t.Fatal("seeded temp row is not an auto-link candidate, want MBID-tagged row (broken fixture)")
	}

	r.scanRoots(ctx, false)

	files, total, err := st.ListFiles(ctx, store.FileListOptions{PageOptions: store.PageOptions{Limit: 100}})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(files) != 1 || files[0].Path != normalPath {
		t.Fatalf("after scan total=%d files=%v, want only %q (temp row pruned)", total, files, normalPath)
	}

	if got, err := os.ReadFile(tempPath); err != nil {
		t.Errorf("temp file missing after scan: %v (must be left alone)", err)
	} else if string(got) != string(wantTemp) {
		t.Error("temp file was modified by the scan, want it untouched")
	}

	dst := canonicalDst(t, st, root, embedTrackID, "wav")
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Errorf("canonical destination %q exists (err=%v), want nothing auto-linked", dst, err)
	}
}

func TestCleanupStaleTemps(t *testing.T) {
	root := t.TempDir()
	r, _ := newEmbedRunner(t, root)

	old := filepath.Join(root, ".coda-tag-old.wav")
	fresh := filepath.Join(root, ".coda-tag-fresh.wav")
	hidden := filepath.Join(root, ".hidden.wav")
	for _, p := range []string{old, fresh, hidden} {
		writeTestWav(t, p)
	}

	aged := time.Now().Add(-25 * time.Hour)
	if err := os.Chtimes(old, aged, aged); err != nil {
		t.Fatal(err)
	}

	r.cleanupStaleTemps(context.Background())

	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("stale temp still present (err=%v), want removed", err)
	}
	for _, p := range []string{fresh, hidden} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("stat %q = %v, want kept", p, err)
		}
	}
}
