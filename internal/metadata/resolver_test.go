package metadata

import (
	"context"
	"fmt"
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

func TestResolveFile(t *testing.T) {
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
		taglib.Title:  {"Blinding Lights (Official Video)"},
		taglib.Artist: {"TheWeekndVEVO"},
	}, 0)
	if err != nil {
		t.Fatalf("failed to write initial tags: %v", err)
	}

	mock := &mockProvider{
		name: "mock",
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
	resolver := NewResolver([]Provider{mock}, log, 0)
	err = resolver.Resolve(context.Background(), []string{path})
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}

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

	mock := &mockProvider{
		name: "mock",
		results: []TrackInfo{
			{
				Title:  "Completely Different Song",
				Artist: "Unknown Artist",
				Album:  "Random Album",
			},
		},
	}

	log := logger.New(false)
	resolver := NewResolver([]Provider{mock}, log, 0)
	resolver.Resolve(context.Background(), []string{path})

	tags, err := taglib.ReadTags(path)
	if err != nil {
		t.Fatalf("failed to read tags: %v", err)
	}

	if got := tags[taglib.Title]; len(got) == 0 || got[0] != "My Song" {
		t.Errorf("title was changed, expected original to be preserved")
	}
}

func TestFallbackToSecondProvider(t *testing.T) {
	p1 := &mockProvider{name: "empty", results: nil}
	p2 := &mockProvider{
		name: "fallback",
		results: []TrackInfo{
			{Title: "My Song", Artist: "My Artist", Album: "My Album", Genre: "Rock"},
		},
	}

	log := logger.New(false)
	r := NewResolver([]Provider{p1, p2}, log, 0.5)

	query := SearchQuery{Title: "My Song", Artist: "My Artist"}
	best, idx := r.findPrimaryMatch(context.Background(), query)

	if !p2.called {
		t.Error("second provider was not consulted")
	}
	if best.Title != "My Song" {
		t.Errorf("best.Title = %q, want %q", best.Title, "My Song")
	}
	if idx != 1 {
		t.Errorf("matchIdx = %d, want 1", idx)
	}
}

func TestGapFilling(t *testing.T) {
	p1 := &mockProvider{
		name: "primary",
		results: []TrackInfo{
			{Title: "My Song", Artist: "My Artist", Album: "My Album", Year: 2020},
		},
	}
	p2 := &mockProvider{
		name: "filler",
		results: []TrackInfo{
			{
				Title:       "My Song",
				Artist:      "My Artist",
				Album:       "My Album",
				Genre:       "Rock",
				TrackNumber: 3,
				ISRC:        "US1234567890",
			},
		},
	}

	log := logger.New(false)
	r := NewResolver([]Provider{p1, p2}, log, 0.5)

	query := SearchQuery{Title: "My Song", Artist: "My Artist"}
	base := TrackInfo{
		Title:  "My Song",
		Artist: "My Artist",
		Album:  "My Album",
		Year:   2020,
	}

	filled := r.fillGaps(context.Background(), query, base, 0)

	if filled.Genre != "Rock" {
		t.Errorf("Genre = %q, want %q", filled.Genre, "Rock")
	}
	if filled.TrackNumber != 3 {
		t.Errorf("TrackNumber = %d, want 3", filled.TrackNumber)
	}
	if filled.ISRC != "US1234567890" {
		t.Errorf("ISRC = %q, want %q", filled.ISRC, "US1234567890")
	}
	// Authoritative fields must not change
	if filled.Year != 2020 {
		t.Errorf("Year = %d, want 2020 (should not be overwritten)", filled.Year)
	}
}

func TestGapFilling_CompleteMatch_SkipsSecondProvider(t *testing.T) {
	p1 := &mockProvider{
		name: "complete",
		results: []TrackInfo{
			{
				Title:       "My Song",
				Artist:      "My Artist",
				Album:       "My Album",
				Genre:       "Pop",
				TrackNumber: 1,
				DiscNumber:  1,
				Year:        2020,
				ISRC:        "US0000000001",
				ArtworkURL:  "https://example.com/art.jpg",
			},
		},
	}
	p2 := &mockProvider{name: "unused"}

	log := logger.New(false)
	r := NewResolver([]Provider{p1, p2}, log, 0.5)

	query := SearchQuery{Title: "My Song", Artist: "My Artist"}
	filled := r.fillGaps(context.Background(), query, p1.results[0], 0)

	if p2.called {
		t.Error("second provider should not be consulted when match is complete")
	}
	if filled.Genre != "Pop" {
		t.Errorf("Genre = %q, want %q", filled.Genre, "Pop")
	}
}

func TestGapFilling_NoProviderFindsMatch(t *testing.T) {
	p1 := &mockProvider{name: "fail1", err: fmt.Errorf("api down")}
	p2 := &mockProvider{name: "fail2", err: fmt.Errorf("api down")}

	log := logger.New(false)
	r := NewResolver([]Provider{p1, p2}, log, 0.5)

	query := SearchQuery{Title: "My Song", Artist: "My Artist"}
	best, _ := r.findPrimaryMatch(context.Background(), query)

	if best.Confidence >= 0.5 {
		t.Errorf("expected no match above threshold, got confidence %.2f", best.Confidence)
	}
}

func TestGapFilling_DoesNotOverwriteAuthoritativeFields(t *testing.T) {
	base := TrackInfo{
		Title:       "Original Title",
		Artist:      "Original Artist",
		Album:       "Original Album",
		AlbumArtist: "Original AlbumArtist",
		Genre:       "",
	}
	filler := TrackInfo{
		Title:       "Different Title",
		Artist:      "Different Artist",
		Album:       "Different Album",
		AlbumArtist: "Different AlbumArtist",
		Genre:       "Jazz",
	}

	merged := mergeTrackInfo(base, filler)

	if merged.Title != "Original Title" {
		t.Errorf("Title = %q, want %q", merged.Title, "Original Title")
	}
	if merged.Artist != "Original Artist" {
		t.Errorf("Artist = %q, want %q", merged.Artist, "Original Artist")
	}
	if merged.Album != "Original Album" {
		t.Errorf("Album = %q, want %q", merged.Album, "Original Album")
	}
	if merged.AlbumArtist != "Original AlbumArtist" {
		t.Errorf("AlbumArtist = %q, want %q", merged.AlbumArtist, "Original AlbumArtist")
	}
	if merged.Genre != "Jazz" {
		t.Errorf("Genre = %q, want %q", merged.Genre, "Jazz")
	}
}

func TestHasMissingFields(t *testing.T) {
	complete := TrackInfo{
		Genre:       "Pop",
		TrackNumber: 1,
		DiscNumber:  1,
		Year:        2020,
		ISRC:        "US0000000001",
		ArtworkURL:  "https://example.com/art.jpg",
	}
	if hasMissingFields(complete) {
		t.Error("expected complete track to have no missing fields")
	}

	incomplete := TrackInfo{Genre: "Pop"}
	if !hasMissingFields(incomplete) {
		t.Error("expected incomplete track to have missing fields")
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
