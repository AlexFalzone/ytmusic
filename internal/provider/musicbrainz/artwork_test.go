package musicbrainz

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"ytmusic/internal/metadata"
)

// Search used to probe Cover Art Archive for every candidate's release. The
// resolver now checks the artwork of the one candidate it keeps, by
// downloading it, so a search is a single request.
func TestSearchDoesNotProbeArtwork(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		respond(t, w, `{"recordings": [`+recordingJSON+`]}`)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	c.artworkBaseURL = srv.URL + "/caa"
	results, err := c.Search(context.Background(), metadata.SearchQuery{Title: "Song", Artist: "Artist"})
	if err != nil || len(results) != 1 {
		t.Fatalf("Search = %v, %v, want one result", results, err)
	}

	if n := requests.Load(); n != 1 {
		t.Errorf("Search sent %d requests, want 1", n)
	}
	if want := srv.URL + "/caa/rel-1/front-500"; results[0].ArtworkURL != want {
		t.Errorf("ArtworkURL = %q, want %q", results[0].ArtworkURL, want)
	}
}
