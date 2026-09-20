package deezer

import (
	"context"
	"os"
	"testing"

	"ytmusic/internal/metadata"
)

// TestLiveSearch talks to the real API, so it is opt-in: run it with
// `LIVE=1 go test ./internal/provider/deezer/`. The mocked tests cannot catch
// BUG-14, where the query syntax itself stopped being understood upstream —
// they assert the string we send, not what Deezer does with it.
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
