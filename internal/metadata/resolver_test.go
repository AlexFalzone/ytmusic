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

func TestMatchTrackByTitle_FindsBestMatch(t *testing.T) {
	tracks := []ReleaseTrack{
		{TrackNumber: 1, DiscNumber: 1, Title: "TRUST!"},
		{TrackNumber: 2, DiscNumber: 1, Title: "DIRTY!"},
		{TrackNumber: 3, DiscNumber: 1, Title: "NEMO!"},
	}
	got, score := matchTrackByTitle("TRUST!", tracks)
	if got.TrackNumber != 1 {
		t.Errorf("TrackNumber = %d, want 1", got.TrackNumber)
	}
	if score < 0.9 {
		t.Errorf("score = %.2f, want >= 0.9", score)
	}
}

func TestMatchTrackByTitle_LowScoreForUnrelated(t *testing.T) {
	tracks := []ReleaseTrack{
		{TrackNumber: 1, Title: "TRUST!"},
		{TrackNumber: 2, Title: "DIRTY!"},
	}
	_, score := matchTrackByTitle("COMPLETELY DIFFERENT SONG", tracks)
	if score > 0.3 {
		t.Errorf("score = %.2f, want <= 0.3 for unrelated title", score)
	}
}

func TestMatchTrackByTitle_NormalizesBeforeComparing(t *testing.T) {
	tracks := []ReleaseTrack{
		{TrackNumber: 5, Title: "ARE U HAPPY?"},
	}
	// yt-dlp may give slightly different casing or punctuation
	got, score := matchTrackByTitle("ARE U HAPPY?", tracks)
	if got.TrackNumber != 5 {
		t.Errorf("TrackNumber = %d, want 5", got.TrackNumber)
	}
	if score < 0.9 {
		t.Errorf("score = %.2f, want >= 0.9 for exact title", score)
	}
}

// newTestMP3 creates a minimal silent MP3 in a temp dir and returns its path.
// Skips the test if ffmpeg is not available.
func newTestMP3(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available")
	}
	path := filepath.Join(t.TempDir(), "test.mp3")
	cmd := exec.Command("ffmpeg", "-f", "lavfi", "-i", "anullsrc=r=44100:cl=mono", "-t", "0.1", "-q:a", "9", path)
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		t.Fatalf("ffmpeg failed: %v", err)
	}
	return path
}

func TestGroupByAlbum_GroupsSameAlbumTogether(t *testing.T) {
	p1 := newTestMP3(t)
	p2 := newTestMP3(t)
	p3 := newTestMP3(t)

	for _, p := range []string{p1, p2} {
		taglib.WriteTags(p, map[string][]string{taglib.Album: {"LP!"}}, 0)
	}
	taglib.WriteTags(p3, map[string][]string{taglib.Album: {"Veteran"}}, 0)

	groups := groupByAlbum([]string{p1, p2, p3})

	if len(groups["LP!"]) != 2 {
		t.Errorf("LP! group size = %d, want 2", len(groups["LP!"]))
	}
	if len(groups["Veteran"]) != 1 {
		t.Errorf("Veteran group size = %d, want 1", len(groups["Veteran"]))
	}
}

func TestGroupByAlbum_FilesWithNoAlbumGetOwnGroup(t *testing.T) {
	p := newTestMP3(t)
	// no album tag written

	groups := groupByAlbum([]string{p})

	total := 0
	for _, files := range groups {
		total += len(files)
	}
	if total != 1 {
		t.Errorf("expected 1 file total across groups, got %d", total)
	}
}

func TestWritePositionalTags_WritesTrackAndDisc(t *testing.T) {
	p := newTestMP3(t)

	if err := writePositionalTags(p, 5, 2); err != nil {
		t.Fatalf("writePositionalTags: %v", err)
	}

	tags, _ := taglib.ReadTags(p)
	if got := firstTag(tags, taglib.TrackNumber); got != "5" {
		t.Errorf("TrackNumber = %q, want %q", got, "5")
	}
	if got := firstTag(tags, taglib.DiscNumber); got != "2" {
		t.Errorf("DiscNumber = %q, want %q", got, "2")
	}
}

func TestWritePositionalTags_SkipsZeroValues(t *testing.T) {
	p := newTestMP3(t)
	taglib.WriteTags(p, map[string][]string{taglib.TrackNumber: {"3"}}, 0)

	// disc = 0 means "unknown", should not write
	if err := writePositionalTags(p, 4, 0); err != nil {
		t.Fatalf("writePositionalTags: %v", err)
	}

	tags, _ := taglib.ReadTags(p)
	if got := firstTag(tags, taglib.TrackNumber); got != "4" {
		t.Errorf("TrackNumber = %q, want %q", got, "4")
	}
	if got := firstTag(tags, taglib.DiscNumber); got != "" {
		t.Errorf("DiscNumber = %q, want empty (zero not written)", got)
	}
}

func TestMergeWithExisting_HandlesSlashTrackNumberFormat(t *testing.T) {
	path := newTestMP3(t)

	if err := taglib.WriteTags(path, map[string][]string{
		taglib.Title:       {"TRUST!"},
		taglib.TrackNumber: {"5/12"},
	}, 0); err != nil {
		t.Fatalf("write initial tags: %v", err)
	}

	// Provider returns track 3 (wrong release), existing tag is "5/12"
	info := TrackInfo{Title: "TRUST!", TrackNumber: 3}
	got := mergeWithExisting(path, info)

	if got.TrackNumber != 5 {
		t.Errorf("TrackNumber = %d, want 5 (slash format must be parsed, not ignored)", got.TrackNumber)
	}
}

func TestMergeWithExisting_PreservesNonZeroTrackNumber(t *testing.T) {
	path := newTestMP3(t)

	if err := taglib.WriteTags(path, map[string][]string{
		taglib.Title:       {"TRUST!"},
		taglib.TrackNumber: {"5"},
	}, 0); err != nil {
		t.Fatalf("write initial tags: %v", err)
	}

	info := TrackInfo{Title: "TRUST!", TrackNumber: 3}
	got := mergeWithExisting(path, info)

	if got.TrackNumber != 5 {
		t.Errorf("TrackNumber = %d, want 5 (yt-dlp value preserved)", got.TrackNumber)
	}
}

func TestMergeWithExisting_FillsZeroTrackNumber(t *testing.T) {
	path := newTestMP3(t)

	if err := taglib.WriteTags(path, map[string][]string{
		taglib.Title: {"TRUST!"},
	}, 0); err != nil {
		t.Fatalf("write initial tags: %v", err)
	}

	info := TrackInfo{Title: "TRUST!", TrackNumber: 5}
	got := mergeWithExisting(path, info)

	if got.TrackNumber != 5 {
		t.Errorf("TrackNumber = %d, want 5 (provider value used when no existing tag)", got.TrackNumber)
	}
}

func TestMergeWithExisting_PreservesNonZeroDiscNumber(t *testing.T) {
	path := newTestMP3(t)

	if err := taglib.WriteTags(path, map[string][]string{
		taglib.Title:      {"TRUST!"},
		taglib.DiscNumber: {"1"},
	}, 0); err != nil {
		t.Fatalf("write initial tags: %v", err)
	}

	info := TrackInfo{Title: "TRUST!", DiscNumber: 2}
	got := mergeWithExisting(path, info)

	if got.DiscNumber != 1 {
		t.Errorf("DiscNumber = %d, want 1 (yt-dlp value preserved)", got.DiscNumber)
	}
}

type mockBatchFingerprinter struct {
	matches []FileMatch
}

func (m *mockBatchFingerprinter) BatchLookupByFiles(_ context.Context, _ []string) []FileMatch {
	return m.matches
}

type mockReleaseResolver struct {
	releaseIDs map[string][]string  // mbid → release IDs
	tracklists map[string]Tracklist // releaseID → tracklist
}

func (m *mockReleaseResolver) ReleaseIDsForRecording(_ context.Context, mbid string) ([]string, error) {
	return m.releaseIDs[mbid], nil
}

func (m *mockReleaseResolver) LookupTracklist(_ context.Context, releaseID string) (Tracklist, error) {
	return m.tracklists[releaseID], nil
}

type mockAlbumResolver struct {
	tracklist Tracklist
	found     bool
	err       error
}

func (m *mockAlbumResolver) ResolveAlbum(_ context.Context, _, _ string) (Tracklist, bool, error) {
	return m.tracklist, m.found, m.err
}

func TestFindDominantRelease_ReturnsMajority(t *testing.T) {
	// 3 MBIDs: mbid-1 and mbid-2 belong to rel-lp, mbid-3 to rel-offline
	// rel-lp count = 2, rel-offline count = 1 → rel-lp is dominant (2 >= 3*0.5 = 1.5)
	rr := &mockReleaseResolver{
		releaseIDs: map[string][]string{
			"mbid-1": {"rel-lp"},
			"mbid-2": {"rel-lp"},
			"mbid-3": {"rel-offline"},
		},
	}

	log := logger.New(false)
	r := NewResolver(nil, log, 0)
	r.WithReleaseResolver(rr)

	dominantID, found := r.findDominantRelease(context.Background(), []string{"mbid-1", "mbid-2", "mbid-3"})
	if !found {
		t.Fatal("expected dominant release to be found")
	}
	if dominantID != "rel-lp" {
		t.Errorf("dominantID = %q, want rel-lp", dominantID)
	}
}

func TestFindDominantRelease_NoQuorum_NotFound(t *testing.T) {
	// Each MBID belongs to a different release — no quorum
	rr := &mockReleaseResolver{
		releaseIDs: map[string][]string{
			"mbid-1": {"rel-a"},
			"mbid-2": {"rel-b"},
			"mbid-3": {"rel-c"},
		},
	}

	log := logger.New(false)
	r := NewResolver(nil, log, 0)
	r.WithReleaseResolver(rr)

	_, found := r.findDominantRelease(context.Background(), []string{"mbid-1", "mbid-2", "mbid-3"})
	if found {
		t.Fatal("expected no dominant release when no quorum")
	}
}

func TestFindDominantRelease_EmptyMBIDs(t *testing.T) {
	rr := &mockReleaseResolver{}

	log := logger.New(false)
	r := NewResolver(nil, log, 0)
	r.WithReleaseResolver(rr)

	_, found := r.findDominantRelease(context.Background(), nil)
	if found {
		t.Fatal("expected no dominant release for empty MBIDs")
	}
}

func TestResolveGroupByFingerprint_WritesPositionalTags(t *testing.T) {
	p1 := newTestMP3(t)
	p2 := newTestMP3(t)

	taglib.WriteTags(p1, map[string][]string{
		taglib.Title:  {"TRUST!"},
		taglib.Artist: {"JPEGMAFIA"},
		taglib.Album:  {"LP!"},
	}, 0)
	taglib.WriteTags(p2, map[string][]string{
		taglib.Title:  {"DIRTY!"},
		taglib.Artist: {"JPEGMAFIA"},
		taglib.Album:  {"LP!"},
	}, 0)

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
				ID:    "rel-lp",
				Title: "LP!",
				Tracks: []ReleaseTrack{
					{TrackNumber: 1, DiscNumber: 1, Title: "TRUST!", MBID: "mbid-1"},
					{TrackNumber: 2, DiscNumber: 1, Title: "DIRTY!", MBID: "mbid-2"},
				},
			},
		},
	}

	log := logger.New(false)
	r := NewResolver(nil, log, 0)
	r.WithBatchFingerprinter(bf)
	r.WithReleaseResolver(rr)

	resolved := r.resolveGroupByFingerprint(context.Background(), []string{p1, p2})

	if len(resolved) != 2 {
		t.Fatalf("resolved %d files, want 2", len(resolved))
	}

	tags1, _ := taglib.ReadTags(p1)
	if got := firstTag(tags1, taglib.TrackNumber); got != "1" {
		t.Errorf("p1 TrackNumber = %q, want 1", got)
	}
	if got := firstTag(tags1, taglib.DiscNumber); got != "1" {
		t.Errorf("p1 DiscNumber = %q, want 1", got)
	}

	tags2, _ := taglib.ReadTags(p2)
	if got := firstTag(tags2, taglib.TrackNumber); got != "2" {
		t.Errorf("p2 TrackNumber = %q, want 2", got)
	}
}

func TestResolveGroupByFingerprint_TooFewFingerprinted_DoesNothing(t *testing.T) {
	p1 := newTestMP3(t)
	p2 := newTestMP3(t)
	p3 := newTestMP3(t)

	// only 1 of 3 files gets a match (< 50%) → should not write any tags
	bf := &mockBatchFingerprinter{
		matches: []FileMatch{{Path: p1, MBID: "mbid-1"}},
	}
	rr := &mockReleaseResolver{
		releaseIDs: map[string][]string{"mbid-1": {"rel-lp"}},
		tracklists: map[string]Tracklist{
			"rel-lp": {Tracks: []ReleaseTrack{{TrackNumber: 1, Title: "TRUST!"}}},
		},
	}

	log := logger.New(false)
	r := NewResolver(nil, log, 0)
	r.WithBatchFingerprinter(bf)
	r.WithReleaseResolver(rr)

	resolved := r.resolveGroupByFingerprint(context.Background(), []string{p1, p2, p3})

	if len(resolved) != 0 {
		t.Errorf("expected 0 resolved, got %d (coverage too low)", len(resolved))
	}

	tags1, _ := taglib.ReadTags(p1)
	if got := firstTag(tags1, taglib.TrackNumber); got != "" {
		t.Errorf("TrackNumber should not be written when coverage < 50%%, got %q", got)
	}
}

func TestResolve_PhaseA_RunsBeforePhaseB(t *testing.T) {
	p1 := newTestMP3(t)
	p2 := newTestMP3(t)

	taglib.WriteTags(p1, map[string][]string{
		taglib.Title:  {"TRUST!"},
		taglib.Artist: {"JPEGMAFIA"},
		taglib.Album:  {"LP!"},
	}, 0)
	taglib.WriteTags(p2, map[string][]string{
		taglib.Title:  {"DIRTY!"},
		taglib.Artist: {"JPEGMAFIA"},
		taglib.Album:  {"LP!"},
	}, 0)

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
	if got := firstTag(tags1, taglib.TrackNumber); got != "1" {
		t.Errorf("p1 TrackNumber = %q, want 1 (Phase A must win over Phase B)", got)
	}
}

func TestResolveGroup_WritesPositionalTags(t *testing.T) {
	p1 := newTestMP3(t)
	p2 := newTestMP3(t)

	taglib.WriteTags(p1, map[string][]string{
		taglib.Title:  {"TRUST!"},
		taglib.Artist: {"JPEGMAFIA"},
		taglib.Album:  {"LP!"},
	}, 0)
	taglib.WriteTags(p2, map[string][]string{
		taglib.Title:  {"DIRTY!"},
		taglib.Artist: {"JPEGMAFIA"},
		taglib.Album:  {"LP!"},
	}, 0)

	ar := &mockAlbumResolver{
		found: true,
		tracklist: Tracklist{
			ID:    "abc",
			Title: "LP!",
			Tracks: []ReleaseTrack{
				{TrackNumber: 1, DiscNumber: 1, Title: "TRUST!"},
				{TrackNumber: 2, DiscNumber: 1, Title: "DIRTY!"},
			},
		},
	}

	log := logger.New(false)
	r := NewResolver(nil, log, 0)
	if err := r.resolveGroup(context.Background(), "LP!", []string{p1, p2}, ar); err != nil {
		t.Fatalf("resolveGroup: %v", err)
	}

	tags1, _ := taglib.ReadTags(p1)
	if got := firstTag(tags1, taglib.TrackNumber); got != "1" {
		t.Errorf("p1 TrackNumber = %q, want %q", got, "1")
	}
	if got := firstTag(tags1, taglib.DiscNumber); got != "1" {
		t.Errorf("p1 DiscNumber = %q, want %q", got, "1")
	}

	tags2, _ := taglib.ReadTags(p2)
	if got := firstTag(tags2, taglib.TrackNumber); got != "2" {
		t.Errorf("p2 TrackNumber = %q, want %q", got, "2")
	}
}

func TestResolveGroup_NotFound_DoesNothing(t *testing.T) {
	p := newTestMP3(t)
	taglib.WriteTags(p, map[string][]string{
		taglib.Title: {"TRUST!"},
		taglib.Album: {"LP!"},
	}, 0)

	ar := &mockAlbumResolver{found: false}

	log := logger.New(false)
	r := NewResolver(nil, log, 0)
	if err := r.resolveGroup(context.Background(), "LP!", []string{p}, ar); err != nil {
		t.Fatalf("resolveGroup: %v", err)
	}

	tags, _ := taglib.ReadTags(p)
	if got := firstTag(tags, taglib.TrackNumber); got != "" {
		t.Errorf("TrackNumber should not be written when not found, got %q", got)
	}
}

func TestResolveGroup_LowTitleMatch_DoesNotWriteTags(t *testing.T) {
	p := newTestMP3(t)
	taglib.WriteTags(p, map[string][]string{
		taglib.Title: {"COMPLETELY DIFFERENT TRACK"},
		taglib.Album: {"LP!"},
	}, 0)

	ar := &mockAlbumResolver{
		found: true,
		tracklist: Tracklist{
			Tracks: []ReleaseTrack{
				{TrackNumber: 1, Title: "TRUST!"},
				{TrackNumber: 2, Title: "DIRTY!"},
			},
		},
	}

	log := logger.New(false)
	r := NewResolver(nil, log, 0)
	if err := r.resolveGroup(context.Background(), "LP!", []string{p}, ar); err != nil {
		t.Fatalf("resolveGroup: %v", err)
	}

	tags, _ := taglib.ReadTags(p)
	if got := firstTag(tags, taglib.TrackNumber); got != "" {
		t.Errorf("TrackNumber should not be written for low match, got %q", got)
	}
}

func TestResolve_AlbumFirstPhaseWritesPositionalTags(t *testing.T) {
	p1 := newTestMP3(t)
	p2 := newTestMP3(t)

	taglib.WriteTags(p1, map[string][]string{
		taglib.Title:  {"TRUST!"},
		taglib.Artist: {"JPEGMAFIA"},
		taglib.Album:  {"LP!"},
	}, 0)
	taglib.WriteTags(p2, map[string][]string{
		taglib.Title:  {"DIRTY!"},
		taglib.Artist: {"JPEGMAFIA"},
		taglib.Album:  {"LP!"},
	}, 0)

	ar := &mockAlbumResolver{
		found: true,
		tracklist: Tracklist{
			Tracks: []ReleaseTrack{
				{TrackNumber: 1, DiscNumber: 1, Title: "TRUST!"},
				{TrackNumber: 2, DiscNumber: 1, Title: "DIRTY!"},
			},
		},
	}

	// Provider returns no results so per-file resolution doesn't overwrite positional tags.
	mock := &mockProvider{name: "empty", results: nil}

	log := logger.New(false)
	r := NewResolver([]Provider{mock}, log, 0.9)
	r.WithAlbumResolver(ar)

	if err := r.Resolve(context.Background(), []string{p1, p2}); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	tags1, _ := taglib.ReadTags(p1)
	if got := firstTag(tags1, taglib.TrackNumber); got != "1" {
		t.Errorf("p1 TrackNumber = %q, want %q", got, "1")
	}

	tags2, _ := taglib.ReadTags(p2)
	if got := firstTag(tags2, taglib.TrackNumber); got != "2" {
		t.Errorf("p2 TrackNumber = %q, want %q", got, "2")
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

	got := firstTag(tags, taglib.TrackNumber)
	if got != "1" {
		t.Errorf("TrackNumber = %q, want %q (yt-dlp value must be preserved)", got, "1")
	}
}
