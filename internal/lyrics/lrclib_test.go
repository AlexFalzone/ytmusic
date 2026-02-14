package lyrics

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetch(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       string
		wantSynced string
		wantPlain  string
		wantErr    bool
	}{
		{
			name:   "synced and plain lyrics",
			status: http.StatusOK,
			body: `{
				"syncedLyrics": "[00:12.00]Hello world",
				"plainLyrics": "Hello world"
			}`,
			wantSynced: "[00:12.00]Hello world",
			wantPlain:  "Hello world",
		},
		{
			name:   "plain only",
			status: http.StatusOK,
			body: `{
				"syncedLyrics": "",
				"plainLyrics": "Just plain text"
			}`,
			wantPlain: "Just plain text",
		},
		{
			name:   "no lyrics",
			status: http.StatusOK,
			body:   `{"syncedLyrics": "", "plainLyrics": ""}`,
		},
		{
			name:   "not found",
			status: http.StatusNotFound,
			body:   `{"code":404,"name":"NotFoundError","message":"Failed to find specified track"}`,
		},
		{
			name:    "server error",
			status:  http.StatusInternalServerError,
			body:    `internal server error`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("User-Agent") != "ytmusic/1.0" {
					t.Errorf("unexpected User-Agent: %s", r.Header.Get("User-Agent"))
				}
				w.WriteHeader(tt.status)
				w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			c := NewClient()
			c.apiURL = srv.URL

			result, err := c.Fetch(context.Background(), "Artist", "Title", "Album")
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.Synced != tt.wantSynced {
				t.Errorf("Synced = %q, want %q", result.Synced, tt.wantSynced)
			}
			if result.Plain != tt.wantPlain {
				t.Errorf("Plain = %q, want %q", result.Plain, tt.wantPlain)
			}
		})
	}
}

func TestFetchQueryParams(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if got := q.Get("artist_name"); got != "The Beatles" {
			t.Errorf("artist_name = %q, want %q", got, "The Beatles")
		}
		if got := q.Get("track_name"); got != "Let It Be" {
			t.Errorf("track_name = %q, want %q", got, "Let It Be")
		}
		if got := q.Get("album_name"); got != "Let It Be" {
			t.Errorf("album_name = %q, want %q", got, "Let It Be")
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := NewClient()
	c.apiURL = srv.URL

	c.Fetch(context.Background(), "The Beatles", "Let It Be", "Let It Be")
}
