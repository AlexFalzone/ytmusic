package metadata

import (
	"testing"
	"time"

	"ytmusic/internal/logger"
)

func TestDurationFits(t *testing.T) {
	s := time.Second
	tests := []struct {
		name            string
		file, candidate time.Duration
		want            bool
	}{
		{"file length unknown", 0, 180 * s, true},
		{"candidate length unknown", 180 * s, 0, true},
		{"same length", 210 * s, 210 * s, true},
		{"shorter within 10%", 189 * s, 210 * s, true},
		{"shorter beyond 10%", 188 * s, 210 * s, false},
		{"short track, shorter within 3s", 23 * s, 25 * s, true},
		{"short track, shorter beyond 3s", 21 * s, 25 * s, false},
		{"longer: music video intro", 270 * s, 210 * s, true},
		{"exactly twice as long", 420 * s, 210 * s, true},
		{"over twice as long", 421 * s, 210 * s, false},
	}
	for _, tt := range tests {
		if got := durationFits(tt.file, tt.candidate); got != tt.want {
			t.Errorf("%s: durationFits(%v, %v) = %v, want %v", tt.name, tt.file, tt.candidate, got, tt.want)
		}
	}
}

func newEvalResolver() *Resolver {
	return NewResolver(nil, logger.New(false), 0.7)
}

func TestEvaluateSkipsOtherVersions(t *testing.T) {
	src := source{query: SearchQuery{Title: "Song", Artist: "Artist"}}

	best, _ := newEvalResolver().evaluate(src, []TrackInfo{
		{Title: "Song - Live at Wembley", Artist: "Artist", Album: "Live"},
		{Title: "Song", Artist: "Artist", Album: "Studio"},
	})

	if best == nil || best.info.Album != "Studio" {
		t.Fatalf("best = %+v, want the studio recording", best)
	}
}

func TestEvaluatePicksDeclaredVersion(t *testing.T) {
	src := source{
		query:   SearchQuery{Title: "Song", Artist: "Artist"},
		version: Version{Key: "live", Label: "Live"},
	}

	best, _ := newEvalResolver().evaluate(src, []TrackInfo{
		{Title: "Song", Artist: "Artist", Album: "Studio"},
		{Title: "Song - Live at Wembley", Artist: "Artist", Album: "Live at Wembley"},
	})

	if best == nil || best.info.Album != "Live at Wembley" {
		t.Fatalf("best = %+v, want the live recording", best)
	}
}

func TestEvaluateVetoesShorterRecording(t *testing.T) {
	src := source{query: SearchQuery{Title: "Song", Artist: "Artist"}, duration: 150 * time.Second}

	best, _ := newEvalResolver().evaluate(src, []TrackInfo{
		{Title: "Song", Artist: "Artist", Duration: 200 * time.Second},
	})

	if best != nil {
		t.Errorf("best = %+v, want nil: a 200s recording is not this 150s file", best)
	}
}

func TestEvaluateBreaksTiesOnDuration(t *testing.T) {
	src := source{query: SearchQuery{Title: "Song", Artist: "Artist"}, duration: 200 * time.Second}

	best, _ := newEvalResolver().evaluate(src, []TrackInfo{
		{Title: "Song", Artist: "Artist", Album: "A", Duration: 230 * time.Second},
		{Title: "Song", Artist: "Artist", Album: "B", Duration: 201 * time.Second},
	})

	if best == nil || best.info.Album != "B" {
		t.Fatalf("best = %+v, want album B, the closest length", best)
	}
}

func TestEvaluateScoresCleanedTitle(t *testing.T) {
	src := source{query: SearchQuery{Title: "Here Comes the Sun", Artist: "The Beatles"}}

	best, _ := newEvalResolver().evaluate(src, []TrackInfo{
		{Title: "Here Comes the Sun - Remastered 2019", Artist: "The Beatles"},
	})

	if best == nil || best.info.Confidence < 0.99 {
		t.Fatalf("best = %+v, want a full-confidence match on the cleaned title", best)
	}
	if best.info.Title != "Here Comes the Sun - Remastered 2019" {
		t.Errorf("title = %q: the provider's title is kept, cleaning is for comparison only", best.info.Title)
	}
}

func TestEvaluateOffersOriginalAsDonor(t *testing.T) {
	src := source{
		query:    SearchQuery{Title: "Blinding Lights", Artist: "The Weeknd"},
		version:  Version{Key: "sped up", Label: "Sped Up"},
		duration: 160 * time.Second,
	}

	exact, donor := newEvalResolver().evaluate(src, []TrackInfo{
		{Title: "Blinding Lights - 2020 Remaster", Artist: "The Weeknd", Duration: 200 * time.Second},
	})

	if exact != nil {
		t.Errorf("exact = %+v, want nil: no provider has the sped-up version", exact)
	}
	// A variant is expected to differ in length: the donor skips that check.
	if donor == nil || !donor.donor || donor.base != "Blinding Lights" {
		t.Fatalf("donor = %+v, want the original with base title %q", donor, "Blinding Lights")
	}
}

func TestEvaluateOffersNoDonorForOriginalFile(t *testing.T) {
	src := source{query: SearchQuery{Title: "Song", Artist: "Artist"}}

	exact, donor := newEvalResolver().evaluate(src, []TrackInfo{
		{Title: "Song - Live", Artist: "Artist"},
	})

	if exact != nil || donor != nil {
		t.Errorf("exact = %+v, donor = %+v, want neither", exact, donor)
	}
}

func TestAsVariant(t *testing.T) {
	original := TrackInfo{
		Title: "Blinding Lights - 2020 Remaster", Artist: "The Weeknd", Album: "After Hours",
		Year: 2020, Genre: "Pop", ArtworkURL: "https://example.com/art.jpg",
		ISRC: "USUG11904206", TrackNumber: 9, TotalTracks: 14, DiscNumber: 1,
	}

	got := asVariant(original, "Blinding Lights", Version{Key: "sped up", Label: "Sped Up"})

	if got.Title != "Blinding Lights (Sped Up)" {
		t.Errorf("Title = %q, want %q", got.Title, "Blinding Lights (Sped Up)")
	}
	if got.Album != "After Hours" || got.Year != 2020 || got.Genre != "Pop" || got.ArtworkURL == "" {
		t.Errorf("descriptive fields lost: %+v", got)
	}
	// These identify the original recording; on a variant they would make it
	// pass for the original and collide with it in the library.
	if got.ISRC != "" || got.TrackNumber != 0 || got.TotalTracks != 0 || got.DiscNumber != 0 {
		t.Errorf("identity fields kept: ISRC=%q track=%d/%d disc=%d", got.ISRC, got.TrackNumber, got.TotalTracks, got.DiscNumber)
	}
}
