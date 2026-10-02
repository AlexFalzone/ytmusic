package itunes

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"ytmusic/internal/buildinfo"
	"ytmusic/internal/metadata"
	"ytmusic/internal/throttle"
)

// setThrottle keeps the tests apart from where the client keeps its throttle.
func setThrottle(c *Client, th *throttle.Throttle) { c.api.Throttle = th }

func newTestClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := New()
	c.apiURL = srv.URL
	setThrottle(c, throttle.New(0))
	return c
}

func TestSearchMapsTheAnswer(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); got != buildinfo.UserAgent() {
			t.Errorf("User-Agent = %q, want %q", got, buildinfo.UserAgent())
		}
		q := r.URL.Query()
		if q.Get("term") != "Song Artist" || q.Get("media") != "music" || q.Get("entity") != "song" || q.Get("limit") != "5" {
			t.Errorf("query = %v", q)
		}
		if _, err := w.Write([]byte(`{"resultCount": 1, "results": [{
			"trackName": "Song", "artistName": "Artist", "collectionName": "Album",
			"primaryGenreName": "Rock", "trackNumber": 3, "discNumber": 2,
			"trackTimeMillis": 215000,
			"artworkUrl100": "https://is1.mzstatic.com/a/100x100bb.jpg",
			"releaseDate": "2019-11-29T08:00:00Z"}]}`)); err != nil {
			t.Errorf("write: %v", err)
		}
	})

	got, err := c.Search(context.Background(), metadata.SearchQuery{Title: "Song", Artist: "Artist"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	want := metadata.TrackInfo{
		Title: "Song", Artist: "Artist", Album: "Album", AlbumArtist: "Artist",
		Genre: "Rock", TrackNumber: 3, DiscNumber: 2,
		ArtworkURL:  "https://is1.mzstatic.com/a/600x600bb.jpg",
		Duration:    215 * time.Second,
		ReleaseDate: "2019-11-29T08:00:00Z", Year: 2019,
	}
	if len(got) != 1 || got[0] != want {
		t.Errorf("Search = %+v, want [%+v]", got, want)
	}
}

func TestSearchReportsAFailedAnswer(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "slow down", http.StatusForbidden)
	})
	if _, err := c.Search(context.Background(), metadata.SearchQuery{Title: "Song"}); err == nil {
		t.Error("Search = nil error, want the 403")
	}
}
