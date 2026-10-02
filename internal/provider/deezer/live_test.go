package deezer

import (
	"context"
	"os"
	"testing"

	"ytmusic/internal/metadata"
)

// Opt-in: LIVE=1 go test ./internal/provider/deezer/. Mocks cannot catch the API changing how it reads the query (BUG-14).
func TestLiveSearch(t *testing.T) {
	if os.Getenv("LIVE") == "" {
		t.Skip("set LIVE=1 to query the real Deezer API")
	}

	queries := []metadata.SearchQuery{
		{Title: "Blinding Lights", Artist: "The Weeknd"},
		{Title: "Money", Artist: "Marracash"},
		{Title: "bad guy", Artist: "Billie Eilish"},
	}

	c := New()
	for _, q := range queries {
		res, err := c.Search(context.Background(), q)
		if err != nil {
			t.Errorf("%q by %q: %v", q.Title, q.Artist, err)
			continue
		}
		if len(res) == 0 {
			t.Errorf("%q by %q: no results", q.Title, q.Artist)
			continue
		}
		t.Logf("%q by %q: %d results, top %q by %q", q.Title, q.Artist, len(res), res[0].Title, res[0].Artist)
	}
}
