package metadata

import (
	"context"
	"fmt"
	"testing"
	"time"

	"ytmusic/internal/logger"

	"go.senan.xyz/taglib"
)

func TestResolveFile(t *testing.T) {
	path := newTestMP3(t)

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
	path := newTestMP3(t)

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
	if err := resolver.Resolve(context.Background(), []string{path}); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

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
	m, ok := r.findPrimaryMatch(context.Background(), source{query: query})

	if !p2.called {
		t.Error("second provider was not consulted")
	}
	if !ok {
		t.Fatal("expected a match")
	}
	if m.info.Title != "My Song" {
		t.Errorf("Title = %q, want %q", m.info.Title, "My Song")
	}
	if m.providerIdx != 1 {
		t.Errorf("providerIdx = %d, want 1", m.providerIdx)
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

	filled := r.fillGaps(context.Background(), source{query: query}, match{info: base}, nil)

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
	filled := r.fillGaps(context.Background(), source{query: query}, match{info: p1.results[0]}, nil)

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
	if _, ok := r.findPrimaryMatch(context.Background(), source{query: query}); ok {
		t.Error("expected no match above threshold")
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

// Providers write the featured artists into the track name; the query has
// already dropped them. Compared raw, the two share 1 token out of 6.
func TestResolveFile_MatchesCandidateWithFeaturingInTitle(t *testing.T) {
	path := newTestMP3(t)
	tagTestFile(t, path, "Peaches", "Justin Bieber")

	resolveOne(t, path, &mockProvider{name: "mock", results: []TrackInfo{
		{Title: "Peaches (feat. Daniel Caesar & Giveon)", Artist: "Justin Bieber", Album: "Justice"},
	}})

	if got := readTestTag(t, path, taglib.Album); got != "Justice" {
		t.Errorf("album = %q, want %q", got, "Justice")
	}
}

func TestResolveFile_PreservesYtdlpTrackNumber(t *testing.T) {
	path := newTestMP3(t)

	if err := taglib.WriteTags(path, map[string][]string{
		taglib.Title:       {"TRUST!"},
		taglib.Artist:      {"JPEGMAFIA"},
		taglib.Album:       {"LP!"},
		taglib.TrackNumber: {"1"},
	}, 0); err != nil {
		t.Fatalf("write initial tags: %v", err)
	}

	// Provider returns a confident match but with a wrong track number (wrong release).
	mock := &mockProvider{
		name: "mock",
		results: []TrackInfo{
			{
				Title:       "TRUST!",
				Artist:      "JPEGMAFIA",
				Album:       "LP! OFFLINE",
				TrackNumber: 7,
				DiscNumber:  1,
				Year:        2022,
			},
		},
	}

	log := logger.New(false)
	resolver := NewResolver([]Provider{mock}, log, 0)
	if err := resolver.Resolve(context.Background(), []string{path}); err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}

	tags, err := taglib.ReadTags(path)
	if err != nil {
		t.Fatalf("read tags: %v", err)
	}

	got := FirstTag(tags, taglib.TrackNumber)
	if got != "1" {
		t.Errorf("TrackNumber = %q, want %q (yt-dlp value must be preserved)", got, "1")
	}
}

// A file with no variant must not pay for extra lookups: the first match wins.
func TestFindPrimaryMatch_StopsAtFirstMatch(t *testing.T) {
	p1 := &mockProvider{name: "first", results: []TrackInfo{{Title: "Song", Artist: "Artist", Album: "Album"}}}
	p2 := &mockProvider{name: "second", results: []TrackInfo{{Title: "Song", Artist: "Artist", Album: "Album"}}}
	r := NewResolver([]Provider{p1, p2}, logger.New(false), 0.7)

	m, ok := r.findPrimaryMatch(context.Background(), source{query: SearchQuery{Title: "Song", Artist: "Artist"}})

	if !ok || m.providerIdx != 0 {
		t.Fatalf("match = %+v, ok = %v, want the first provider's", m, ok)
	}
	if p2.called {
		t.Error("second provider consulted after the first matched")
	}
}

func TestGapFilling_IgnoresFillerOfAnotherVersion(t *testing.T) {
	p1 := &mockProvider{name: "primary"}
	p2 := &mockProvider{name: "filler", results: []TrackInfo{
		{Title: "Song - Live", Artist: "Artist", Album: "Album", Genre: "Rock"},
	}}
	r := NewResolver([]Provider{p1, p2}, logger.New(false), 0.5)

	filled := r.fillGaps(context.Background(),
		source{query: SearchQuery{Title: "Song", Artist: "Artist"}},
		match{info: TrackInfo{Title: "Song", Artist: "Artist"}}, nil)

	if filled.Genre != "" {
		t.Errorf("Genre = %q: a live recording must not fill the studio one", filled.Genre)
	}
}

// A donor is completed by other originals, never by the variant it stands in for.
func TestGapFilling_DonorTakesOriginalAsFiller(t *testing.T) {
	p1 := &mockProvider{name: "primary"}
	p2 := &mockProvider{name: "filler", results: []TrackInfo{
		{Title: "Song - Live", Artist: "Artist", Album: "Album", Genre: "Jazz"},
		{Title: "Song", Artist: "Artist", Album: "Album", Genre: "Rock"},
	}}
	r := NewResolver([]Provider{p1, p2}, logger.New(false), 0.5)

	filled := r.fillGaps(context.Background(), liveSource(),
		match{info: TrackInfo{Title: "Song", Artist: "Artist"}, donor: true}, nil)

	if filled.Genre != "Rock" {
		t.Errorf("Genre = %q, want %q", filled.Genre, "Rock")
	}
}

func TestResolveFile_DoesNotTagOriginalAsLiveVersion(t *testing.T) {
	path := newTestMP3(t)
	tagTestFile(t, path, "Song", "Artist")

	resolveOne(t, path, &mockProvider{name: "mock", results: []TrackInfo{
		{Title: "Song - Live at Wembley", Artist: "Artist", Album: "Live at Wembley"},
	}})

	if got := readTestTag(t, path, taglib.Album); got != "" {
		t.Errorf("album = %q, want untouched", got)
	}
}

// Both tests below use the file's real length: if it were not read, the first
// would tag the file and fail.
func TestResolveFile_ShorterRecordingIsRejected(t *testing.T) {
	path := newTestMP3Len(t, "5")
	tagTestFile(t, path, "Song", "Artist")

	resolveOne(t, path, &mockProvider{name: "mock", results: []TrackInfo{
		{Title: "Song", Artist: "Artist", Album: "Album", Duration: 60 * time.Second},
	}})

	if got := readTestTag(t, path, taglib.Album); got != "" {
		t.Errorf("album = %q: a 60s recording cannot be this 5s file", got)
	}
}

func TestResolveFile_RecordingOfSameLengthIsAccepted(t *testing.T) {
	path := newTestMP3Len(t, "5")
	tagTestFile(t, path, "Song", "Artist")

	resolveOne(t, path, &mockProvider{name: "mock", results: []TrackInfo{
		{Title: "Song", Artist: "Artist", Album: "Album", Duration: 6 * time.Second},
	}})

	if got := readTestTag(t, path, taglib.Album); got != "Album" {
		t.Errorf("album = %q, want %q", got, "Album")
	}
}

func liveSource() source {
	return source{
		query:   SearchQuery{Title: "Song", Artist: "Artist"},
		version: Version{Key: "live", Label: "Live"},
	}
}

// Before settling for a donor, every provider gets the chance to offer the
// variant itself.
func TestFindPrimaryMatch_PrefersExactVariantFromLaterProvider(t *testing.T) {
	p1 := &mockProvider{name: "studio-only", results: []TrackInfo{{Title: "Song", Artist: "Artist", Album: "Studio"}}}
	p2 := &mockProvider{name: "has-live", results: []TrackInfo{{Title: "Song - Live", Artist: "Artist", Album: "Live"}}}
	r := NewResolver([]Provider{p1, p2}, logger.New(false), 0.7)

	m, ok := r.findPrimaryMatch(context.Background(), liveSource())

	if !ok || m.donor || m.info.Album != "Live" || m.providerIdx != 1 {
		t.Fatalf("match = %+v, ok = %v, want the live recording from the second provider", m, ok)
	}
}

func TestFindPrimaryMatch_FallsBackToDonor(t *testing.T) {
	p1 := &mockProvider{name: "studio-only", results: []TrackInfo{{Title: "Song", Artist: "Artist", Album: "Studio"}}}
	p2 := &mockProvider{name: "empty"}
	r := NewResolver([]Provider{p1, p2}, logger.New(false), 0.7)

	m, ok := r.findPrimaryMatch(context.Background(), liveSource())

	if !p2.called {
		t.Error("second provider not consulted before settling for a donor")
	}
	if !ok || !m.donor || m.info.Album != "Studio" || m.providerIdx != 0 {
		t.Fatalf("match = %+v, ok = %v, want the first provider's studio recording as donor", m, ok)
	}
}

func TestResolveFile_VariantBorrowsOriginalMetadata(t *testing.T) {
	path := newTestMP3(t)
	tagTestFile(t, path, "Blinding Lights (Sped Up)", "The Weeknd")

	resolveOne(t, path, &mockProvider{name: "mock", results: []TrackInfo{{
		Title: "Blinding Lights", Artist: "The Weeknd", Album: "After Hours",
		ISRC: "USUG11904206", TrackNumber: 9, Year: 2020, Duration: 200 * time.Second,
	}}})

	if got := readTestTag(t, path, taglib.Title); got != "Blinding Lights (Sped Up)" {
		t.Errorf("title = %q, want %q", got, "Blinding Lights (Sped Up)")
	}
	if got := readTestTag(t, path, taglib.Album); got != "After Hours" {
		t.Errorf("album = %q, want %q", got, "After Hours")
	}
	if got := readTestTag(t, path, taglib.ISRC); got != "" {
		t.Errorf("ISRC = %q: the original's ISRC does not identify the variant", got)
	}
	if got := readTestTag(t, path, taglib.TrackNumber); got != "" {
		t.Errorf("track number = %q: the variant is not that track of the album", got)
	}
}
