package musicbrainz

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"ytmusic/internal/metadata"
)

func newTestClient(url string) *Client {
	return &Client{
		httpClient:  &http.Client{Timeout: 5 * time.Second},
		apiURL:      url,
		lastRequest: time.Now().Add(-2 * time.Second), // avoid rate limit in tests
	}
}

func TestSearch_ParsesResponse(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/recording", func(w http.ResponseWriter, r *http.Request) {
		if ua := r.Header.Get("User-Agent"); ua == "" {
			t.Error("missing User-Agent header")
		}
		w.Header().Set("Content-Type", "application/json")
		// artwork URL will be rewritten to point to this test server
		w.Write([]byte(`{
			"recordings": [{
				"id": "rec-1",
				"title": "Bohemian Rhapsody",
				"length": 354000,
				"artist-credit": [{"artist": {"id": "a1", "name": "Queen"}}],
				"releases": [{
					"id": "rel-1",
					"title": "A Night at the Opera",
					"date": "1975-10-31",
					"artist-credit": [{"artist": {"id": "a1", "name": "Queen"}}],
					"media": [{"position": 1, "track-count": 12, "track": [{"number": "11", "position": 11}]}]
				}],
				"isrcs": ["GBUM71029604"]
			}]
		}`))
	})
	mux.HandleFunc("/release/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestClient(srv.URL)
	// Override artwork base URL to point to test server
	c.artworkBaseURL = srv.URL + "/release"
	results, err := c.Search(context.Background(), metadata.SearchQuery{
		Title:  "Bohemian Rhapsody",
		Artist: "Queen",
	})
	if err != nil {
		t.Fatalf("Search() error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	r := results[0]
	if r.Title != "Bohemian Rhapsody" {
		t.Errorf("Title = %q, want %q", r.Title, "Bohemian Rhapsody")
	}
	if r.Artist != "Queen" {
		t.Errorf("Artist = %q, want %q", r.Artist, "Queen")
	}
	if r.Album != "A Night at the Opera" {
		t.Errorf("Album = %q, want %q", r.Album, "A Night at the Opera")
	}
	if r.AlbumArtist != "Queen" {
		t.Errorf("AlbumArtist = %q, want %q", r.AlbumArtist, "Queen")
	}
	if r.Year != 1975 {
		t.Errorf("Year = %d, want 1975", r.Year)
	}
	if r.TrackNumber != 11 {
		t.Errorf("TrackNumber = %d, want 11", r.TrackNumber)
	}
	if r.TotalTracks != 12 {
		t.Errorf("TotalTracks = %d, want 12", r.TotalTracks)
	}
	if r.DiscNumber != 1 {
		t.Errorf("DiscNumber = %d, want 1", r.DiscNumber)
	}
	if r.ISRC != "GBUM71029604" {
		t.Errorf("ISRC = %q, want %q", r.ISRC, "GBUM71029604")
	}
	if r.Duration != 354*time.Second {
		t.Errorf("Duration = %v, want %v", r.Duration, 354*time.Second)
	}
	wantArtwork := srv.URL + "/release/rel-1/front-500"
	if r.ArtworkURL != wantArtwork {
		t.Errorf("ArtworkURL = %q, want %q", r.ArtworkURL, wantArtwork)
	}
}

func TestSearch_EmptyQuery(t *testing.T) {
	c := newTestClient("http://unused")
	results, err := c.Search(context.Background(), metadata.SearchQuery{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if results != nil {
		t.Errorf("expected nil results for empty query, got %v", results)
	}
}

func TestSearch_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal error"))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	_, err := c.Search(context.Background(), metadata.SearchQuery{Title: "test"})
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
}

func TestSearch_RetryOn429(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"recordings": [{"id": "r1", "title": "Test", "artist-credit": [{"artist": {"name": "Artist"}}]}]}`))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	results, err := c.Search(context.Background(), metadata.SearchQuery{Title: "Test"})
	if err != nil {
		t.Fatalf("Search() error: %v", err)
	}
	if calls != 2 {
		t.Errorf("expected 2 calls (1 retry), got %d", calls)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}
}

func TestSearch_MultipleArtistCredits(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"recordings": [{
				"id": "rec-2",
				"title": "Under Pressure",
				"length": 248000,
				"artist-credit": [
					{"artist": {"id": "a1", "name": "Queen"}},
					{"artist": {"id": "a2", "name": "David Bowie"}}
				]
			}]
		}`))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	results, err := c.Search(context.Background(), metadata.SearchQuery{Title: "Under Pressure"})
	if err != nil {
		t.Fatalf("Search() error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Artist != "Queen, David Bowie" {
		t.Errorf("Artist = %q, want %q", results[0].Artist, "Queen, David Bowie")
	}
}

func TestPickBestRelease(t *testing.T) {
	compilation := release{
		ID:     "comp-1",
		Title:  "Sanremo 2023",
		Status: "Official",
		Date:   "2023-02-10",
		ReleaseGroup: releaseGroup{
			PrimaryType:    "Album",
			SecondaryTypes: []string{"Compilation"},
		},
	}
	album := release{
		ID:     "album-1",
		Title:  "Sirio",
		Status: "Official",
		Date:   "2022-06-03",
		ReleaseGroup: releaseGroup{
			PrimaryType: "Album",
		},
	}
	single := release{
		ID:     "single-1",
		Title:  "CENERE",
		Status: "Official",
		Date:   "2023-01-01",
		ReleaseGroup: releaseGroup{
			PrimaryType: "Single",
		},
	}
	bootleg := release{
		ID:     "boot-1",
		Title:  "Live Bootleg",
		Status: "Bootleg",
		Date:   "2020-01-01",
		ReleaseGroup: releaseGroup{
			PrimaryType: "Album",
		},
	}

	tests := []struct {
		name     string
		releases []release
		wantID   string
	}{
		{
			name:     "album over compilation",
			releases: []release{compilation, album},
			wantID:   "album-1",
		},
		{
			name:     "album over single",
			releases: []release{single, album},
			wantID:   "album-1",
		},
		{
			name:     "official over bootleg",
			releases: []release{bootleg, album},
			wantID:   "album-1",
		},
		{
			name:     "compilation first still picks album",
			releases: []release{compilation, single, album},
			wantID:   "album-1",
		},
		{
			name: "earlier date wins at same score",
			releases: []release{
				{ID: "newer", Title: "B", Status: "Official", Date: "2023-01-01", ReleaseGroup: releaseGroup{PrimaryType: "Album"}},
				{ID: "older", Title: "A", Status: "Official", Date: "2020-01-01", ReleaseGroup: releaseGroup{PrimaryType: "Album"}},
			},
			wantID: "older",
		},
		{
			name: "year-only date treated as Jan 1, not preferred over full earlier date",
			releases: []release{
				// "2020" means somewhere in 2020; "2020-03-15" is a known earlier-in-year date
				// Both same score; the month-precision one should win (more precise and earlier)
				{ID: "partial", Title: "A", Status: "Official", Date: "2020", ReleaseGroup: releaseGroup{PrimaryType: "Album"}},
				{ID: "precise", Title: "B", Status: "Official", Date: "2020-03-15", ReleaseGroup: releaseGroup{PrimaryType: "Album"}},
			},
			// "2020" padded to "2020-01-01" < "2020-03-15" → partial wins as "earlier"
			wantID: "partial",
		},
		{
			name: "year-month date compared correctly against full date",
			releases: []release{
				{ID: "full", Title: "A", Status: "Official", Date: "2020-03-01", ReleaseGroup: releaseGroup{PrimaryType: "Album"}},
				{ID: "yearmonth", Title: "B", Status: "Official", Date: "2020-01", ReleaseGroup: releaseGroup{PrimaryType: "Album"}},
			},
			// "2020-01" padded to "2020-01-01" < "2020-03-01" → yearmonth wins
			wantID: "yearmonth",
		},
		{
			name: "release with track data beats earlier date without",
			releases: []release{
				{
					ID: "regular", Title: "Album", Status: "Official", Date: "2020-01-01",
					ReleaseGroup: releaseGroup{PrimaryType: "Album"},
					// no Media: this recording is not on this release
				},
				{
					ID: "deluxe", Title: "Album (Deluxe Edition)", Status: "Official", Date: "2020-06-01",
					ReleaseGroup: releaseGroup{PrimaryType: "Album"},
					Media:        []media{{Position: 1, TrackCount: 14, Track: []track{{Number: "13", Position: 13}}}},
				},
			},
			wantID: "deluxe",
		},
		{
			name:     "single release returns it",
			releases: []release{compilation},
			wantID:   "comp-1",
		},
	}

	albumVariantTests := []struct {
		name     string
		releases []release
		prefer   string
		wantID   string
	}{
		{
			name: "prefers release matching query album over variant",
			releases: []release{
				{ID: "offline", Title: "LP! OFFLINE", Status: "Official", Date: "2022-01-01", ReleaseGroup: releaseGroup{PrimaryType: "Album"}, Media: []media{{Position: 1, Track: []track{{Number: "4", Position: 4}}}}},
				{ID: "standard", Title: "LP!", Status: "Official", Date: "2021-10-22", ReleaseGroup: releaseGroup{PrimaryType: "Album"}, Media: []media{{Position: 1, Track: []track{{Number: "4", Position: 4}}}}},
			},
			prefer: "LP!",
			wantID: "standard",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pickBestRelease(tt.releases, "")
			if got.ID != tt.wantID {
				t.Errorf("pickBestRelease() picked %q (%s), want %q", got.Title, got.ID, tt.wantID)
			}
		})
	}

	for _, tt := range albumVariantTests {
		t.Run(tt.name, func(t *testing.T) {
			got := pickBestRelease(tt.releases, tt.prefer)
			if got.ID != tt.wantID {
				t.Errorf("pickBestRelease() picked %q (%s), want %q", got.Title, got.ID, tt.wantID)
			}
		})
	}
}

func TestBuildQuery(t *testing.T) {
	tests := []struct {
		name  string
		query metadata.SearchQuery
		want  string
	}{
		{
			name:  "title and artist",
			query: metadata.SearchQuery{Title: "Test", Artist: "Artist"},
			want:  `recording:"Test" AND artist:"Artist"`,
		},
		{
			name:  "title only",
			query: metadata.SearchQuery{Title: "Test"},
			want:  `recording:"Test"`,
		},
		{
			name:  "empty",
			query: metadata.SearchQuery{},
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildQuery(tt.query)
			if got != tt.want {
				t.Errorf("buildQuery() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLookupByMBID_Found(t *testing.T) {
	recording := map[string]any{
		"id":     "mbid-abc",
		"title":  "Test Track",
		"length": 240000,
		"artist-credit": []map[string]any{
			{"artist": map[string]any{"id": "artist-1", "name": "Test Artist"}},
		},
		"releases": []map[string]any{
			{
				"id":     "release-1",
				"title":  "Test Album",
				"status": "Official",
				"date":   "2020-06-01",
				"artist-credit": []map[string]any{
					{"artist": map[string]any{"id": "artist-1", "name": "Test Artist"}},
				},
				"release-group": map[string]any{
					"primary-type":    "Album",
					"secondary-types": []any{},
				},
				"media": []map[string]any{
					{"track": []map[string]any{{"number": "3"}}},
				},
			},
		},
		"isrcs": []string{"USUM72000001"},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(recording)
	}))
	defer srv.Close()

	client := NewWithURL(srv.URL, "https://coverartarchive.org/release")
	info, err := client.LookupByMBID(context.Background(), "mbid-abc", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Title != "Test Track" {
		t.Errorf("title: got %q, want %q", info.Title, "Test Track")
	}
	if info.Artist != "Test Artist" {
		t.Errorf("artist: got %q, want %q", info.Artist, "Test Artist")
	}
	if info.Album != "Test Album" {
		t.Errorf("album: got %q, want %q", info.Album, "Test Album")
	}
	if info.TrackNumber != 3 {
		t.Errorf("track number: got %d, want 3", info.TrackNumber)
	}
	if info.Year != 2020 {
		t.Errorf("year: got %d, want 2020", info.Year)
	}
	if info.ISRC != "USUM72000001" {
		t.Errorf("ISRC: got %q, want %q", info.ISRC, "USUM72000001")
	}
}

func TestSearchRelease_ReturnsCandidates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/release" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"releases": [
				{
					"id": "release-lp",
					"title": "LP!",
					"date": "2021-10-22",
					"artist-credit": [{"artist": {"id": "a1", "name": "JPEGMAFIA"}}],
					"release-group": {"primary-type": "Album", "secondary-types": []}
				}
			]
		}`))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	candidates, err := c.searchRelease(context.Background(), "LP!", "JPEGMAFIA")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("got %d candidates, want 1", len(candidates))
	}
	if candidates[0].ID != "release-lp" {
		t.Errorf("ID = %q, want release-lp", candidates[0].ID)
	}
	if candidates[0].Title != "LP!" {
		t.Errorf("Title = %q, want LP!", candidates[0].Title)
	}
}

func TestSearchRelease_EmptyResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"releases": []}`))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	candidates, err := c.searchRelease(context.Background(), "Nonexistent Album", "Nobody")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(candidates) != 0 {
		t.Errorf("expected 0 candidates, got %d", len(candidates))
	}
}

func TestLookupRelease_ReturnsTracklist(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"id": "release-lp",
			"title": "LP!",
			"artist-credit": [{"artist": {"id": "a1", "name": "JPEGMAFIA"}}],
			"media": [
				{
					"position": 1,
					"track-count": 3,
					"tracks": [
						{"number": "1", "position": 1, "title": "TRUST!", "recording": {"id": "rec-1"}},
						{"number": "2", "position": 2, "title": "DIRTY!", "recording": {"id": "rec-2"}},
						{"number": "3", "position": 3, "title": "NEMO!",  "recording": {"id": "rec-3"}}
					]
				}
			]
		}`))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	tl, err := c.lookupRelease(context.Background(), "release-lp")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tl.ID != "release-lp" {
		t.Errorf("ID = %q, want release-lp", tl.ID)
	}
	if tl.Artist != "JPEGMAFIA" {
		t.Errorf("Artist = %q, want JPEGMAFIA", tl.Artist)
	}
	if len(tl.Tracks) != 3 {
		t.Fatalf("got %d tracks, want 3", len(tl.Tracks))
	}
	if tl.Tracks[0].TrackNumber != 1 || tl.Tracks[0].DiscNumber != 1 || tl.Tracks[0].Title != "TRUST!" {
		t.Errorf("track 0 = %+v, unexpected", tl.Tracks[0])
	}
	if tl.Tracks[2].MBID != "rec-3" {
		t.Errorf("track 2 MBID = %q, want rec-3", tl.Tracks[2].MBID)
	}
}

func TestLookupRelease_MultiDisc(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"id": "release-multi",
			"title": "Double Album",
			"artist-credit": [{"artist": {"id": "a1", "name": "Artist"}}],
			"media": [
				{
					"position": 1,
					"tracks": [
						{"number": "1", "position": 1, "title": "Side A Track 1", "recording": {"id": "r1"}}
					]
				},
				{
					"position": 2,
					"tracks": [
						{"number": "1", "position": 1, "title": "Side B Track 1", "recording": {"id": "r2"}}
					]
				}
			]
		}`))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	tl, err := c.lookupRelease(context.Background(), "release-multi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tl.Tracks) != 2 {
		t.Fatalf("got %d tracks, want 2", len(tl.Tracks))
	}
	if tl.Tracks[0].DiscNumber != 1 {
		t.Errorf("track 0 DiscNumber = %d, want 1", tl.Tracks[0].DiscNumber)
	}
	if tl.Tracks[1].DiscNumber != 2 {
		t.Errorf("track 1 DiscNumber = %d, want 2", tl.Tracks[1].DiscNumber)
	}
}

func TestResolveAlbum_ReturnsBestMatchingTracklist(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/release", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"releases": [{"id": "r1", "title": "LP!", "date": "2021-10-22", "artist-credit": [{"artist": {"id": "a1", "name": "JPEGMAFIA"}}], "release-group": {"primary-type": "Album", "secondary-types": []}}]}`))
	})
	mux.HandleFunc("/release/r1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id": "r1", "title": "LP!", "artist-credit": [{"artist": {"id": "a1", "name": "JPEGMAFIA"}}], "media": [{"position": 1, "tracks": [{"number": "1", "position": 1, "title": "TRUST!", "recording": {"id": "rec-1"}}]}]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestClient(srv.URL)
	tl, found, err := c.ResolveAlbum(context.Background(), "LP!", "JPEGMAFIA")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found {
		t.Fatal("expected found=true")
	}
	if tl.Title != "LP!" {
		t.Errorf("Title = %q, want LP!", tl.Title)
	}
	if len(tl.Tracks) != 1 || tl.Tracks[0].Title != "TRUST!" {
		t.Errorf("unexpected tracks: %+v", tl.Tracks)
	}
}

func TestResolveAlbum_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"releases": []}`))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	_, found, err := c.ResolveAlbum(context.Background(), "Ghost Album", "Unknown")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found {
		t.Fatal("expected found=false for empty results")
	}
}

func TestReleaseIDsForRecording_ReturnsIDs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"id": "rec-1",
			"title": "TRUST!",
			"releases": [
				{"id": "rel-lp", "title": "LP!"},
				{"id": "rel-offline", "title": "LP! OFFLINE"}
			]
		}`))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	ids, err := c.ReleaseIDsForRecording(context.Background(), "rec-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("got %d IDs, want 2", len(ids))
	}
	if ids[0] != "rel-lp" || ids[1] != "rel-offline" {
		t.Errorf("IDs = %v, want [rel-lp rel-offline]", ids)
	}
}

func TestReleaseIDsForRecording_NoReleases(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id": "rec-x", "title": "Unknown", "releases": []}`))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	ids, err := c.ReleaseIDsForRecording(context.Background(), "rec-x")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("expected empty IDs, got %v", ids)
	}
}

func TestLookupTracklist_DelegatesToLookupRelease(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"id": "rel-lp",
			"title": "LP!",
			"artist-credit": [{"artist": {"id": "a1", "name": "JPEGMAFIA"}}],
			"media": [{
				"position": 1,
				"tracks": [
					{"number": "1", "position": 1, "title": "TRUST!", "recording": {"id": "rec-1"}}
				]
			}]
		}`))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	tl, err := c.LookupTracklist(context.Background(), "rel-lp")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tl.ID != "rel-lp" {
		t.Errorf("ID = %q, want rel-lp", tl.ID)
	}
	if len(tl.Tracks) != 1 || tl.Tracks[0].Title != "TRUST!" {
		t.Errorf("unexpected tracks: %+v", tl.Tracks)
	}
}
