package itunes

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"ytmusic/internal/metadata"
	"ytmusic/internal/throttle"
)

func TestSearchWaitsForTheThrottle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(`{"resultCount": 0, "results": []}`)); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	defer srv.Close()

	c := New()
	c.apiURL = srv.URL
	setThrottle(c, throttle.New(100*time.Millisecond))
	start := time.Now()
	for range 3 {
		if _, err := c.Search(t.Context(), metadata.SearchQuery{Title: "Song"}); err != nil {
			t.Fatalf("Search: %v", err)
		}
	}
	if elapsed := time.Since(start); elapsed < 200*time.Millisecond {
		t.Errorf("three searches took %v, want at least 200ms", elapsed)
	}
}

// Apple counts requests per address: two clients each spacing their own would together exceed the limit.
func TestClientsShareTheRateLimit(t *testing.T) {
	a, b := New(), New()
	if a.api.Throttle != b.api.Throttle {
		t.Error("two clients have their own throttles, want the one shared limit")
	}
}
