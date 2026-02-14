package musicbrainz

import (
	"context"
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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/recording" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if ua := r.Header.Get("User-Agent"); ua == "" {
			t.Error("missing User-Agent header")
		}

		w.Header().Set("Content-Type", "application/json")
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
					"media": [{"track": [{"number": "11"}]}]
				}],
				"isrcs": ["GBUM71029604"]
			}]
		}`))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
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
	if r.ISRC != "GBUM71029604" {
		t.Errorf("ISRC = %q, want %q", r.ISRC, "GBUM71029604")
	}
	if r.Duration != 354*time.Second {
		t.Errorf("Duration = %v, want %v", r.Duration, 354*time.Second)
	}
	if r.ArtworkURL != "https://coverartarchive.org/release/rel-1/front-500" {
		t.Errorf("ArtworkURL = %q", r.ArtworkURL)
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
