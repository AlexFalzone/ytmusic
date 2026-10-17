package metadata

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ytmusic/internal/logger"
	"ytmusic/internal/testaudio"

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
	err   error
}

func (s *stubFingerprinter) LookupByFile(_ context.Context, _, _ string) (TrackInfo, bool, error) {
	return s.info, s.found, s.err
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

func newTestMP3Len(t *testing.T, seconds string) string {
	t.Helper()
	return testaudio.MP3(t, t.TempDir(), "test.mp3", seconds)
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
	if err := r.Resolve(context.Background(), []string{path}, nil); err != nil {
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

	if err := r.Resolve(context.Background(), []string{p1, p2}, nil); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	tags1, _ := taglib.ReadTags(p1)
	if got := FirstTag(tags1, taglib.TrackNumber); got != "1" {
		t.Errorf("p1 TrackNumber = %q, want 1 (Phase A must win over Phase B)", got)
	}
}

// The log as a file, read back after the run.
func fileLogger(t *testing.T) (*logger.Logger, func() string) {
	t.Helper()
	log := logger.New(false)
	path := filepath.Join(t.TempDir(), "run.log")
	if err := log.SetFileLog(path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = log.Close() }) // the test has already read what it needed
	return log, func() string {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
}

func warnings(log, containing string) int {
	n := 0
	for line := range strings.SplitSeq(log, "\n") {
		if strings.Contains(line, "[WARN]") && strings.Contains(line, containing) {
			n++
		}
	}
	return n
}

func TestFingerprintOffIsLoggedOnce(t *testing.T) {
	var files []string
	for i := range 8 {
		path := newTestMP3(t)
		tagTestFile(t, path, fmt.Sprintf("Song %d", i), "Band")
		files = append(files, path)
	}
	log, read := fileLogger(t)
	r := NewResolver([]Provider{&mockProvider{name: "empty"}}, log, 0).WithWorkers(4)
	r.WithFingerprinter(&stubFingerprinter{err: fmt.Errorf("%w: rejected key", ErrFingerprintOff)})

	if err := r.Resolve(context.Background(), files, nil); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := warnings(read(), "off for the rest"); got != 1 {
		t.Errorf("%d warnings about fingerprinting being off, want 1", got)
	}
}

func TestFingerprintErrorIsLoggedPerFile(t *testing.T) {
	path := newTestMP3(t)
	tagTestFile(t, path, "Song", "Band")
	log, read := fileLogger(t)
	r := NewResolver([]Provider{&mockProvider{name: "empty"}}, log, 0)
	r.WithFingerprinter(&stubFingerprinter{err: errors.New("fpcalc: exit status 3")})

	if err := r.Resolve(context.Background(), []string{path}, nil); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	out := read()
	if warnings(out, path) != 1 || warnings(out, "fpcalc: exit status 3") != 1 {
		t.Errorf("log = %q, want one warning naming the file and the fpcalc error", out)
	}
}

// A cancel kills every fpcalc in flight: their errors say nothing about the files.
func TestFingerprintErrorIsQuietWhenCancelled(t *testing.T) {
	log, read := fileLogger(t)
	r := NewResolver(nil, log, 0).WithFingerprinter(&stubFingerprinter{err: errors.New("fpcalc: signal: killed")})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, found := r.fingerprintMatch(ctx, "/a.mp3", ""); found {
		t.Error("found a match from a failed lookup")
	}
	if got := warnings(read(), ""); got != 0 {
		t.Errorf("%d warnings after a cancel, want 0", got)
	}
}
