package metadata

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"

	"ytmusic/internal/logger"

	"go.senan.xyz/taglib"
)

// mockProvider implements provider.Provider for testing.
type mockProvider struct {
	results []TrackInfo
	err     error
}

func (m *mockProvider) Name() string { return "mock" }
func (m *mockProvider) Search(_ context.Context, _ SearchQuery) ([]TrackInfo, error) {
	return m.results, m.err
}

func TestResolveFile(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "test.mp3")

	// Create test audio file with initial tags
	cmd := exec.Command("ffmpeg", "-f", "lavfi", "-i", "anullsrc=r=44100:cl=mono", "-t", "0.1", "-q:a", "9", path)
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// Write initial tags (simulating yt-dlp metadata)
	err := taglib.WriteTags(path, map[string][]string{
		taglib.Title:  {"Blinding Lights (Official Video)"},
		taglib.Artist: {"TheWeekndVEVO"},
	}, 0)
	if err != nil {
		t.Fatalf("failed to write initial tags: %v", err)
	}

	mock := &mockProvider{
		results: []TrackInfo{
			{
				Title:       "Blinding Lights",
				Artist:      "The Weeknd",
				Album:       "After Hours",
				AlbumArtist: "The Weeknd",
				TrackNumber: 9,
				Year:        2020,
				Genre:       "Pop",
			},
		},
	}

	log := logger.New(false)
	resolver := NewResolver(mock, log, 0)
	err = resolver.Resolve(context.Background(), []string{path})
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}

	// Verify tags were updated
	tags, err := taglib.ReadTags(path)
	if err != nil {
		t.Fatalf("failed to read tags: %v", err)
	}

	checks := map[string]string{
		taglib.Title:       "Blinding Lights",
		taglib.Artist:      "The Weeknd",
		taglib.Album:       "After Hours",
		taglib.AlbumArtist: "The Weeknd",
	}

	for key, want := range checks {
		got := ""
		if vals, ok := tags[key]; ok && len(vals) > 0 {
			got = vals[0]
		}
		if got != want {
			t.Errorf("tag %s = %q, want %q", key, got, want)
		}
	}
}

func TestResolveFileLowConfidence(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "test.mp3")
	cmd := exec.Command("ffmpeg", "-f", "lavfi", "-i", "anullsrc=r=44100:cl=mono", "-t", "0.1", "-q:a", "9", path)
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	err := taglib.WriteTags(path, map[string][]string{
		taglib.Title:  {"My Song"},
		taglib.Artist: {"My Artist"},
	}, 0)
	if err != nil {
		t.Fatalf("failed to write initial tags: %v", err)
	}

	// Return a completely unrelated result → low confidence
	mock := &mockProvider{
		results: []TrackInfo{
			{
				Title:  "Completely Different Song",
				Artist: "Unknown Artist",
				Album:  "Random Album",
			},
		},
	}

	log := logger.New(false)
	resolver := NewResolver(mock, log, 0)
	resolver.Resolve(context.Background(), []string{path})

	// Original tags should be preserved
	tags, err := taglib.ReadTags(path)
	if err != nil {
		t.Fatalf("failed to read tags: %v", err)
	}

	if got := tags[taglib.Title]; len(got) == 0 || got[0] != "My Song" {
		t.Errorf("title was changed, expected original to be preserved")
	}
}

func TestScore(t *testing.T) {
	tests := []struct {
		name      string
		query     SearchQuery
		result    TrackInfo
		wantAbove float64
		wantBelow float64
	}{
		{
			name:      "exact match",
			query:     SearchQuery{Title: "Blinding Lights", Artist: "The Weeknd"},
			result:    TrackInfo{Title: "Blinding Lights", Artist: "The Weeknd"},
			wantAbove: 0.99,
		},
		{
			name:      "title match different artist",
			query:     SearchQuery{Title: "Blinding Lights", Artist: "The Weeknd"},
			result:    TrackInfo{Title: "Blinding Lights", Artist: "Some Other Artist"},
			wantAbove: 0.5,
			wantBelow: 0.8,
		},
		{
			name:      "completely different",
			query:     SearchQuery{Title: "Blinding Lights", Artist: "The Weeknd"},
			result:    TrackInfo{Title: "Bohemian Rhapsody", Artist: "Queen"},
			wantBelow: 0.1,
		},
		{
			name:      "no artist in query",
			query:     SearchQuery{Title: "Blinding Lights"},
			result:    TrackInfo{Title: "Blinding Lights", Artist: "The Weeknd"},
			wantAbove: 0.99,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := score(tt.query, tt.result)
			if tt.wantAbove > 0 && got < tt.wantAbove {
				t.Errorf("score = %.4f, want above %.4f", got, tt.wantAbove)
			}
			if tt.wantBelow > 0 && got > tt.wantBelow {
				t.Errorf("score = %.4f, want below %.4f", got, tt.wantBelow)
			}
		})
	}
}

func TestSimilarity(t *testing.T) {
	tests := []struct {
		a, b string
		want float64
	}{
		{"blinding lights", "blinding lights", 1.0},
		{"", "", 1.0},
		{"something", "", 0.0},
		{"", "something", 0.0},
	}

	for _, tt := range tests {
		got := similarity(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("similarity(%q, %q) = %.4f, want %.4f", tt.a, tt.b, got, tt.want)
		}
	}
}
