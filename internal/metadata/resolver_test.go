package metadata

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"

	"ytmusic/internal/logger"

	"go.senan.xyz/taglib"
)

type mockProvider struct {
	name    string
	results []TrackInfo
	err     error
	called  bool
}

func (m *mockProvider) Name() string { return m.name }

func (m *mockProvider) Search(_ context.Context, _ SearchQuery) ([]TrackInfo, error) {
	m.called = true
	return m.results, m.err
}

type stubFingerprinter struct {
	info  TrackInfo
	found bool
}

func (s *stubFingerprinter) LookupByFile(_ context.Context, _, _ string) (TrackInfo, bool, error) {
	return s.info, s.found, nil
}

func TestResolver_WithFingerprinter_NotNil(t *testing.T) {
	r := NewResolver(nil, nil, 0)
	r2 := r.WithFingerprinter(&stubFingerprinter{})
	if r2 == nil {
		t.Fatal("WithFingerprinter returned nil")
	}
}

func newTestMP3(t *testing.T) string {
	t.Helper()
	return newTestMP3Len(t, "0.1")
}

// newTestMP3Len creates a silent MP3 lasting the given number of seconds.
// Skips the test if ffmpeg is not available.
func newTestMP3Len(t *testing.T, seconds string) string {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available")
	}
	path := filepath.Join(t.TempDir(), "test.mp3")
	cmd := exec.Command("ffmpeg", "-f", "lavfi", "-i", "anullsrc=r=44100:cl=mono", "-t", seconds, "-q:a", "9", path)
	if err := cmd.Run(); err != nil {
		t.Fatalf("ffmpeg failed: %v", err)
	}
	return path
}

func tagTestFile(t *testing.T, path, title, artist string) {
	t.Helper()
	writeTestTags(t, path, map[string][]string{taglib.Title: {title}, taglib.Artist: {artist}})
}

func writeTestTags(t *testing.T, path string, tags map[string][]string) {
	t.Helper()
	if err := taglib.WriteTags(path, tags, 0); err != nil {
		t.Fatalf("write tags: %v", err)
	}
}

func readTestTag(t *testing.T, path, key string) string {
	t.Helper()
	tags, err := taglib.ReadTags(path)
	if err != nil {
		t.Fatalf("read tags: %v", err)
	}
	return FirstTag(tags, key)
}

func resolveOne(t *testing.T, path string, providers ...Provider) {
	t.Helper()
	r := NewResolver(providers, logger.New(false), 0)
	if err := r.Resolve(context.Background(), []string{path}); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
}

func TestResolve_PhaseA_RunsBeforePhaseB(t *testing.T) {
	p1 := newTestMP3(t)
	p2 := newTestMP3(t)

	writeTestTags(t, p1, map[string][]string{
		taglib.Title:  {"TRUST!"},
		taglib.Artist: {"JPEGMAFIA"},
		taglib.Album:  {"LP!"},
	})
	writeTestTags(t, p2, map[string][]string{
		taglib.Title:  {"DIRTY!"},
		taglib.Artist: {"JPEGMAFIA"},
		taglib.Album:  {"LP!"},
	})

	// Phase A resolves both files with correct positions
	bf := &mockBatchFingerprinter{
		matches: []FileMatch{
			{Path: p1, MBID: "mbid-1"},
			{Path: p2, MBID: "mbid-2"},
		},
	}
	rr := &mockReleaseResolver{
		releaseIDs: map[string][]string{
			"mbid-1": {"rel-lp"},
			"mbid-2": {"rel-lp"},
		},
		tracklists: map[string]Tracklist{
			"rel-lp": {
				Tracks: []ReleaseTrack{
					{TrackNumber: 1, DiscNumber: 1, Title: "TRUST!"},
					{TrackNumber: 2, DiscNumber: 1, Title: "DIRTY!"},
				},
			},
		},
	}

	// Phase B would assign wrong positions if it ran for these files
	tar := &mockAlbumResolver{found: true, tracklist: Tracklist{
		Tracks: []ReleaseTrack{
			{TrackNumber: 7, DiscNumber: 1, Title: "TRUST!"},
			{TrackNumber: 8, DiscNumber: 1, Title: "DIRTY!"},
		},
	}}

	mock := &mockProvider{name: "empty", results: nil}
	log := logger.New(false)
	r := NewResolver([]Provider{mock}, log, 0.9)
	r.WithBatchFingerprinter(bf)
	r.WithReleaseResolver(rr)
	r.WithAlbumResolver(tar)

	if err := r.Resolve(context.Background(), []string{p1, p2}); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	// Phase A wrote correct positions; Phase B should have been skipped for these files
	tags1, _ := taglib.ReadTags(p1)
	if got := FirstTag(tags1, taglib.TrackNumber); got != "1" {
		t.Errorf("p1 TrackNumber = %q, want 1 (Phase A must win over Phase B)", got)
	}
}
