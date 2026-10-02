package spotify

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"ytmusic/internal/buildinfo"
	"ytmusic/internal/metadata"
	"ytmusic/internal/testhttp"
)

func TestSearch(t *testing.T) {
	mux := http.NewServeMux()

	mux.HandleFunc("/api/token", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); got != buildinfo.UserAgent() {
			t.Errorf("User-Agent = %q, want %q", got, buildinfo.UserAgent())
		}
		if r.Method != http.MethodPost {
			t.Errorf("token: expected POST, got %s", r.Method)
		}
		user, pass, ok := r.BasicAuth()
		if !ok || user != "test-id" || pass != "test-secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		testhttp.JSON(t, w, tokenResponse{
			AccessToken: "test-token",
			ExpiresIn:   3600,
		})
	})

	mux.HandleFunc("/v1/search", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); got != buildinfo.UserAgent() {
			t.Errorf("User-Agent = %q, want %q", got, buildinfo.UserAgent())
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		q := r.URL.Query().Get("q")
		if q == "" {
			http.Error(w, "missing q", http.StatusBadRequest)
			return
		}

		resp := searchResponse{}
		resp.Tracks.Items = []trackItem{
			{
				Name:        "Blinding Lights",
				Artists:     []artist{{Name: "The Weeknd"}},
				TrackNumber: 9,
				DiscNumber:  1,
				DurationMs:  200040,
				ExternalIDs: externalID{ISRC: "USUG12000497"},
				Album: albumInfo{
					Name:        "After Hours",
					Artists:     []artist{{Name: "The Weeknd"}},
					ReleaseDate: "2020-03-20",
					TotalTracks: 14,
					Images:      []image{{URL: "https://i.scdn.co/image/test"}},
				},
			},
		}
		testhttp.JSON(t, w, resp)
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	client := New("test-id", "test-secret")
	client.tokenURL = server.URL + "/api/token"
	client.apiURL = server.URL + "/v1"

	results, err := client.Search(context.Background(), metadata.SearchQuery{
		Title:  "Blinding Lights",
		Artist: "The Weeknd",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	r := results[0]
	if r.Title != "Blinding Lights" {
		t.Errorf("title = %q, want %q", r.Title, "Blinding Lights")
	}
	if r.Artist != "The Weeknd" {
		t.Errorf("artist = %q, want %q", r.Artist, "The Weeknd")
	}
	if r.Album != "After Hours" {
		t.Errorf("album = %q, want %q", r.Album, "After Hours")
	}
	if r.Year != 2020 {
		t.Errorf("year = %d, want 2020", r.Year)
	}
	if r.TrackNumber != 9 {
		t.Errorf("track = %d, want 9", r.TrackNumber)
	}
	if r.TotalTracks != 14 {
		t.Errorf("total_tracks = %d, want 14", r.TotalTracks)
	}
	if r.ISRC != "USUG12000497" {
		t.Errorf("isrc = %q, want %q", r.ISRC, "USUG12000497")
	}
	if r.ArtworkURL != "https://i.scdn.co/image/test" {
		t.Errorf("artwork = %q", r.ArtworkURL)
	}
}

func TestSearchEmptyQuery(t *testing.T) {
	client := New("id", "secret")
	results, err := client.Search(context.Background(), metadata.SearchQuery{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if results != nil {
		t.Errorf("expected nil results for empty query, got %d", len(results))
	}
}

func TestTokenCaching(t *testing.T) {
	tokenCalls := 0
	mux := http.NewServeMux()

	mux.HandleFunc("/api/token", func(w http.ResponseWriter, r *http.Request) {
		tokenCalls++
		testhttp.JSON(t, w, tokenResponse{
			AccessToken: "cached-token",
			ExpiresIn:   3600,
		})
	})

	mux.HandleFunc("/v1/search", func(w http.ResponseWriter, r *http.Request) {
		resp := searchResponse{}
		testhttp.JSON(t, w, resp)
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	client := New("id", "secret")
	client.tokenURL = server.URL + "/api/token"
	client.apiURL = server.URL + "/v1"

	for _, title := range []string{"a", "b"} {
		if _, err := client.Search(context.Background(), metadata.SearchQuery{Title: title}); err != nil {
			t.Fatalf("Search(%q): %v", title, err)
		}
	}

	if tokenCalls != 1 {
		t.Errorf("expected 1 token call, got %d", tokenCalls)
	}
}

func TestBuildSearchQuery(t *testing.T) {
	tests := []struct {
		name  string
		query metadata.SearchQuery
		want  string
	}{
		{
			name:  "title and artist",
			query: metadata.SearchQuery{Title: "Blinding Lights", Artist: "The Weeknd"},
			want:  "track:Blinding Lights artist:The Weeknd",
		},
		{
			name:  "title only",
			query: metadata.SearchQuery{Title: "Blinding Lights"},
			want:  "track:Blinding Lights",
		},
		{
			name:  "all fields",
			query: metadata.SearchQuery{Title: "Blinding Lights", Artist: "The Weeknd", Album: "After Hours"},
			want:  "track:Blinding Lights artist:The Weeknd album:After Hours",
		},
		{
			name:  "empty",
			query: metadata.SearchQuery{},
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildSearchQuery(tt.query)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSearchAddsTheArtistsGenres(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/token", func(w http.ResponseWriter, r *http.Request) {
		testhttp.JSON(t, w, tokenResponse{AccessToken: "test-token", ExpiresIn: 3600})
	})
	mux.HandleFunc("/v1/search", func(w http.ResponseWriter, r *http.Request) {
		resp := searchResponse{}
		resp.Tracks.Items = []trackItem{{Name: "Song", Artists: []artist{{ID: "a1", Name: "Artist"}}}}
		testhttp.JSON(t, w, resp)
	})
	mux.HandleFunc("/v1/artists/a1", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("Authorization = %q", got)
		}
		testhttp.JSON(t, w, artistResponse{Genres: []string{"hip hop", "pop rap", "trap", "rap"}})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client := New("id", "secret")
	client.tokenURL = server.URL + "/api/token"
	client.apiURL = server.URL + "/v1"

	results, err := client.Search(context.Background(), metadata.SearchQuery{Title: "Song"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 || results[0].Genre != "Hip Hop, Pop Rap, Trap" {
		t.Errorf("results = %+v, want one with genre %q", results, "Hip Hop, Pop Rap, Trap")
	}
}
