package itunes

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"ytmusic/internal/metadata"
	"ytmusic/internal/throttle"
)

// Apple limits the Search API to about 20 calls a minute; the per-file workers
// would otherwise call it several times a second.
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
	c.throttle = throttle.New(100 * time.Millisecond)
	start := time.Now()
	for range 3 {
		if _, err := c.Search(context.Background(), metadata.SearchQuery{Title: "Song"}); err != nil {
			t.Fatalf("Search: %v", err)
		}
	}
	if elapsed := time.Since(start); elapsed < 200*time.Millisecond {
		t.Errorf("three searches took %v, want at least 200ms", elapsed)
	}
}
