package store

import (
	"errors"
	"io"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/ppablomunoz/coda/internal/tags"
	"github.com/ppablomunoz/coda/internal/utils"
)

var errTestRead = errors.New("test tag read failure")

func itoa(i int) string { return strconv.Itoa(i) }

func testBenchLog() *utils.Logger { return utils.NewWithWriter(io.Discard) }

// TestSyncIncrementalSkipsTagReads is the #46 regression: a settled rescan
// must not re-read tags. The first sync reads once to store evidence; the
// second sync with identical size+mtime skips the parse entirely.
func TestSyncIncrementalSkipsTagReads(t *testing.T) {
	root := t.TempDir()
	st := newTestStore(t, root)

	calls := atomic.Int32{}
	prev := syncReadTags
	syncReadTags = func(path string) (*tags.Snapshot, error) {
		calls.Add(1)
		return &tags.Snapshot{MBReleaseTrackID: testTrackID}, nil
	}
	defer func() { syncReadTags = prev }()

	files := []ScannedFile{
		{LibraryRoot: root, Path: root + "/a.mp3", SizeBytes: 100, MtimeNs: 1000, FormatHint: "mp3"},
		{LibraryRoot: root, Path: root + "/b.mp3", SizeBytes: 200, MtimeNs: 2000, FormatHint: "mp3"},
	}
	stats, err := st.SyncLocalFiles(ctx(), files, "2024-06-01 00:00:00", SyncOptions{})
	if err != nil {
		t.Fatalf("first SyncLocalFiles = %v", err)
	}
	if calls.Load() != 2 || stats.TagReads != 2 {
		t.Fatalf("first sync reads = %d/%d, want 2/2 (evidence stored once)", calls.Load(), stats.TagReads)
	}

	// Second scan, identical stat: no tag reads, all unchanged.
	calls.Store(0)
	stats2, err := st.SyncLocalFiles(ctx(), files, "2024-06-02 00:00:00", SyncOptions{})
	if err != nil {
		t.Fatalf("second SyncLocalFiles = %v", err)
	}
	if calls.Load() != 0 || stats2.TagReads != 0 {
		t.Errorf("settled rescan reads = %d/%d, want 0/0", calls.Load(), stats2.TagReads)
	}
	if stats2.Unchanged != 2 {
		t.Errorf("Unchanged = %d, want 2", stats2.Unchanged)
	}
	if len(stats2.Failed) != 0 {
		t.Errorf("Failed = %v, want empty", stats2.Failed)
	}

	// A changed file re-reads exactly once.
	calls.Store(0)
	files[0].SizeBytes = 101
	stats3, err := st.SyncLocalFiles(ctx(), files, "2024-06-03 00:00:00", SyncOptions{})
	if err != nil {
		t.Fatalf("changed SyncLocalFiles = %v", err)
	}
	if calls.Load() != 1 || stats3.TagReads != 1 {
		t.Errorf("changed sync reads = %d/%d, want 1/1", calls.Load(), stats3.TagReads)
	}
	if stats3.Unchanged != 1 {
		t.Errorf("Unchanged = %d, want 1 (only the settled file)", stats3.Unchanged)
	}
}

// TestSyncFailedReadRetries ensures a tag failure clears tags_read_at so the
// next scan retries even when size+mtime match.
func TestSyncFailedReadRetries(t *testing.T) {
	root := t.TempDir()
	st := newTestStore(t, root)

	prev := syncReadTags
	defer func() { syncReadTags = prev }()
	syncReadTags = func(path string) (*tags.Snapshot, error) {
		return nil, errTestRead
	}

	f := ScannedFile{LibraryRoot: root, Path: root + "/x.mp3", SizeBytes: 10, MtimeNs: 5, FormatHint: "mp3"}
	stats, err := st.SyncLocalFiles(ctx(), []ScannedFile{f}, "2024-06-01 00:00:00", SyncOptions{})
	if err != nil {
		t.Fatalf("SyncLocalFiles = %v", err)
	}
	if len(stats.Failed) != 1 {
		t.Fatalf("Failed = %v, want 1", stats.Failed)
	}

	// Same stat, but tags_read_at is NULL so it must retry (and fail again).
	stats2, err := st.SyncLocalFiles(ctx(), []ScannedFile{f}, "2024-06-02 00:00:00", SyncOptions{})
	if err != nil {
		t.Fatalf("retry SyncLocalFiles = %v", err)
	}
	if len(stats2.Failed) != 1 || stats2.TagReads != 1 {
		t.Errorf("retry Failed=%v TagReads=%d, want 1 failed with 1 read (no skip)", stats2.Failed, stats2.TagReads)
	}
}

// TestAutoLinkCandidatesAfterImport is the #46 acceptance: files with
// unresolved MBIDs become candidates once the track is imported.
func TestAutoLinkCandidatesAfterImport(t *testing.T) {
	root := t.TempDir()
	st := newTestStore(t, root)

	prev := syncReadTags
	syncReadTags = func(path string) (*tags.Snapshot, error) {
		return &tags.Snapshot{MBReleaseTrackID: testTrackID}, nil
	}
	defer func() { syncReadTags = prev }()

	f := ScannedFile{LibraryRoot: root, Path: root + "/song.mp3", SizeBytes: 100, MtimeNs: 1000, FormatHint: "mp3"}
	if _, err := st.SyncLocalFiles(ctx(), []ScannedFile{f}, "2024-06-01 00:00:00", SyncOptions{}); err != nil {
		t.Fatalf("SyncLocalFiles = %v", err)
	}
	// No track yet: no candidate.
	cands, err := st.ListAutoLinkCandidates(ctx(), 0, 100)
	if err != nil {
		t.Fatalf("ListAutoLinkCandidates = %v", err)
	}
	if len(cands) != 0 {
		t.Fatalf("candidates before import = %v, want none", cands)
	}

	// Import the track: the same file becomes a candidate without re-reading tags.
	_ = st.CreateArtist(ctx(), Artist{ID: testArtistID, Name: "Test Artist"})
	if err := st.SaveDiscography(ctx(), []FullReleaseParams{testRelease(testTrackID, testRelID, testGroupID)}); err != nil {
		t.Fatalf("SaveDiscography = %v", err)
	}
	cands, err = st.ListAutoLinkCandidates(ctx(), 0, 100)
	if err != nil {
		t.Fatalf("ListAutoLinkCandidates after import = %v", err)
	}
	if len(cands) != 1 || cands[0].MbTrackID != testTrackID {
		t.Fatalf("candidates after import = %+v, want 1 with %q", cands, testTrackID)
	}
}

// TestSyncBatchedUpsert covers >1 chunk (500) to prove multi-row upserts
// handle large libraries without per-row statements.
func TestSyncBatchedUpsert(t *testing.T) {
	root := t.TempDir()
	st := newTestStore(t, root)

	prev := syncReadTags
	syncReadTags = func(path string) (*tags.Snapshot, error) {
		return &tags.Snapshot{}, nil
	}
	defer func() { syncReadTags = prev }()

	const n = 1200
	files := make([]ScannedFile, 0, n)
	for i := 0; i < n; i++ {
		files = append(files, ScannedFile{
			LibraryRoot: root,
			Path:        root + "/f" + itoa(i) + ".mp3",
			SizeBytes:   int64(100 + i), MtimeNs: int64(1000 + i), FormatHint: "mp3",
		})
	}
	stats, err := st.SyncLocalFiles(ctx(), files, "2024-06-01 00:00:00", SyncOptions{})
	if err != nil {
		t.Fatalf("SyncLocalFiles batched = %v", err)
	}
	if stats.Upserted != n || stats.TagReads != n {
		t.Errorf("batched Upserted=%d TagReads=%d, want %d/%d", stats.Upserted, stats.TagReads, n, n)
	}
	// Settled rescan of the whole batch reads nothing.
	stats2, err := st.SyncLocalFiles(ctx(), files, "2024-06-02 00:00:00", SyncOptions{})
	if err != nil {
		t.Fatalf("rescan batched = %v", err)
	}
	if stats2.TagReads != 0 || stats2.Unchanged != n {
		t.Errorf("rescan TagReads=%d Unchanged=%d, want 0/%d", stats2.TagReads, stats2.Unchanged, n)
	}
}

func BenchmarkSyncSettled(b *testing.B) {
	root := b.TempDir()
	log := testBenchLog()
	st, err := NewStore(root+"/bench.db", []string{root}, log)
	if err != nil {
		b.Fatalf("NewStore = %v", err)
	}
	b.Cleanup(st.Close)
	if err := st.AddRootDirs(ctx()); err != nil {
		b.Fatalf("AddRootDirs = %v", err)
	}
	prev := syncReadTags
	syncReadTags = func(path string) (*tags.Snapshot, error) {
		return &tags.Snapshot{}, nil
	}
	defer func() { syncReadTags = prev }()

	const n = 2000
	files := make([]ScannedFile, 0, n)
	for i := 0; i < n; i++ {
		files = append(files, ScannedFile{
			LibraryRoot: root,
			Path:        root + "/f" + itoa(i) + ".mp3",
			SizeBytes:   int64(100 + i), MtimeNs: int64(1000 + i), FormatHint: "mp3",
		})
	}
	if _, err := st.SyncLocalFiles(ctx(), files, "2024-06-01 00:00:00", SyncOptions{}); err != nil {
		b.Fatalf("seed SyncLocalFiles = %v", err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := st.SyncLocalFiles(ctx(), files, "2024-06-02 00:00:00", SyncOptions{}); err != nil {
			b.Fatalf("SyncLocalFiles = %v", err)
		}
	}
}
