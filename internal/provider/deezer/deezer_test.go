package deezer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"ytmusic/internal/buildinfo"
	"ytmusic/internal/metadata"
	"ytmusic/internal/testhttp"
)

func TestSearch(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != buildinfo.UserAgent() {
			t.Errorf("unexpected User-Agent: %s", r.Header.Get("User-Agent"))
		}
		testhttp.JSON(t, w, searchResponse{
			Data: []trackItem{
				{
					TitleShort:    "Santeria",
					ISRC:          "ITXXX1700001",
					Duration:      240,
					TrackPosition: 3,
					DiskNumber:    1,
					Artist:        artist{Name: "Marracash"},
					Album: albumInfo{
						Title:    "Santeria",
						CoverBig: "https://example.com/cover-big.jpg",
						CoverXL:  "https://example.com/cover-xl.jpg",
					},
				},
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := New()
	c.apiURL = srv.URL

	results, err := c.Search(context.Background(), metadata.SearchQuery{
		Title:  "Santeria",
		Artist: "Marracash",
		Album:  "Santeria",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	r := results[0]
	if r.Title != "Santeria" {
		t.Errorf("Title = %q, want %q", r.Title, "Santeria")
	}
	if r.Artist != "Marracash" {
		t.Errorf("Artist = %q, want %q", r.Artist, "Marracash")
	}
	if r.Album != "Santeria" {
		t.Errorf("Album = %q, want %q", r.Album, "Santeria")
	}
	if r.ISRC != "ITXXX1700001" {
		t.Errorf("ISRC = %q, want %q", r.ISRC, "ITXXX1700001")
	}
	if r.ArtworkURL != "https://example.com/cover-xl.jpg" {
		t.Errorf("ArtworkURL = %q, want cover-xl", r.ArtworkURL)
	}
	if r.Duration.Seconds() != 240 {
		t.Errorf("Duration = %v, want 4m0s", r.Duration)
	}
	if r.TrackNumber != 3 {
		t.Errorf("TrackNumber = %d, want 3", r.TrackNumber)
	}
	if r.DiscNumber != 1 {
		t.Errorf("DiscNumber = %d, want 1", r.DiscNumber)
	}
}

func TestSearchEmptyQuery(t *testing.T) {
	c := New()
	results, err := c.Search(context.Background(), metadata.SearchQuery{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if results != nil {
		t.Errorf("expected nil results for empty query, got %d", len(results))
	}
}

func TestSearchNoResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		testhttp.JSON(t, w, searchResponse{Data: []trackItem{}})
	}))
	defer srv.Close()

	c := New()
	c.apiURL = srv.URL

	results, err := c.Search(context.Background(), metadata.SearchQuery{Title: "nonexistent"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestSearchAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		testhttp.JSON(t, w, searchResponse{
			Error: &apiError{Message: "Quota exceeded"},
		})
	}))
	defer srv.Close()

	c := New()
	c.apiURL = srv.URL

	_, err := c.Search(context.Background(), metadata.SearchQuery{Title: "test"})
	if err == nil {
		t.Fatal("expected error for API error response")
	}
}

func TestBuildQuery(t *testing.T) {
	tests := []struct {
		name  string
		query metadata.SearchQuery
		want  string
	}{
		{
			name:  "all fields",
			query: metadata.SearchQuery{Title: "Santeria", Artist: "Marracash", Album: "Santeria"},
			want:  "Santeria Marracash Santeria",
		},
		{
			name:  "title only",
			query: metadata.SearchQuery{Title: "Santeria"},
			want:  "Santeria",
		},
		{
			name:  "title and artist",
			query: metadata.SearchQuery{Title: "Money", Artist: "Marracash"},
			want:  "Money Marracash",
		},
		{
			name:  "no field prefixes",
			query: metadata.SearchQuery{Title: "Blinding Lights", Artist: "The Weeknd"},
			want:  "Blinding Lights The Weeknd",
		},
		{
			name:  "quotes stripped so the query cannot become a phrase",
			query: metadata.SearchQuery{Title: `Say "Hello"`, Artist: "Nobody"},
			want:  "Say Hello Nobody",
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

func TestParseTitleShort(t *testing.T) {
	var item trackItem
	body := `{"title": "Salvador Dalí (Live @ Santeria Tour 2017)", "title_short": "Salvador Dalí"}`
	if err := json.Unmarshal([]byte(body), &item); err != nil {
		t.Fatal(err)
	}
	results := parseResults([]trackItem{item})
	if results[0].Title != "Salvador Dalí" {
		t.Errorf("expected TitleShort, got %q", results[0].Title)
	}
}
