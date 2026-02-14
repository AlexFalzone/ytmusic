package deezer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"ytmusic/internal/metadata"
)

func TestSearch(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != "ytmusic/1.0" {
			t.Errorf("unexpected User-Agent: %s", r.Header.Get("User-Agent"))
		}
		json.NewEncoder(w).Encode(searchResponse{
			Data: []trackItem{
				{
					ID:         1,
					Title:      "Santeria",
					TitleShort: "Santeria",
					ISRC:       "ITXXX1700001",
					Duration:   240,
					Artist:     artist{ID: 100, Name: "Marracash"},
					Album: albumInfo{
						ID:       200,
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
		json.NewEncoder(w).Encode(searchResponse{Data: []trackItem{}})
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
		json.NewEncoder(w).Encode(searchResponse{
			Error: &apiError{Type: "Exception", Message: "Quota exceeded", Code: 4},
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
			want:  `track:"Santeria" artist:"Marracash" album:"Santeria"`,
		},
		{
			name:  "title only",
			query: metadata.SearchQuery{Title: "Santeria"},
			want:  `track:"Santeria"`,
		},
		{
			name:  "title and artist",
			query: metadata.SearchQuery{Title: "Money", Artist: "Marracash"},
			want:  `track:"Money" artist:"Marracash"`,
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
	items := []trackItem{
		{
			Title:      "Salvador Dalí (Live @ Santeria Tour 2017)",
			TitleShort: "Salvador Dalí",
			Artist:     artist{Name: "Marracash"},
			Album:      albumInfo{Title: "Santeria"},
		},
	}
	results := parseResults(items)
	if results[0].Title != "Salvador Dalí" {
		t.Errorf("expected TitleShort, got %q", results[0].Title)
	}
}
