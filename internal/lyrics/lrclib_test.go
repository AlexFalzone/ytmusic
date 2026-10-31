package lyrics

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"ytmusic/internal/buildinfo"
	"ytmusic/internal/testhttp"
	"ytmusic/internal/throttle"
)

func init() { limiter = throttle.New(0) }

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
				if r.Header.Get("User-Agent") != buildinfo.UserAgent() {
					t.Errorf("unexpected User-Agent: %s", r.Header.Get("User-Agent"))
				}
				w.WriteHeader(tt.status)
				if _, err := w.Write([]byte(tt.body)); err != nil {
					t.Errorf("writing fake response: %v", err)
				}
			}))
			defer srv.Close()

			c := NewClient(5 * time.Second)
			c.apiURL = srv.URL

			result, err := c.Fetch(t.Context(), "Artist", "Title", "Album")
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

	c := NewClient(5 * time.Second)
	c.apiURL = srv.URL

	if _, err := c.Fetch(t.Context(), "The Beatles", "Let It Be", "Let It Be"); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
}

func TestFetchRetriesWhenOverloaded(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		testhttp.JSON(t, w, apiResponse{PlainLyrics: "Hello world"})
	}))
	defer srv.Close()

	c := NewClient(5 * time.Second)
	c.apiURL = srv.URL

	result, err := c.Fetch(t.Context(), "Artist", "Title", "Album")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if result.Plain != "Hello world" {
		t.Errorf("Plain = %q, want %q", result.Plain, "Hello world")
	}
}

func TestClientsShareOneThrottle(t *testing.T) {
	a, b := NewClient(time.Second), NewClient(time.Second)
	if a.api.Throttle == nil || a.api.Throttle != b.api.Throttle {
		t.Error("concurrent jobs must split one rate")
	}
}
