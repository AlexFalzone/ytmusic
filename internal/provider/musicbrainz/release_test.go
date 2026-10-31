package musicbrainz

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"ytmusic/internal/throttle"
)

func TestFindReleasesFiltersOnTheTrackCountItselfAndLooksUpEachCandidate(t *testing.T) {
	var lookups atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/release", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		for _, part := range []string{`release:"The Divine Feminine"`, `artist:"Mac Miller"`} {
			if !strings.Contains(q, part) {
				t.Errorf("query %q lacks %s", q, part)
			}
		}
		// The index's "tracks" misses releases: for Legacy, two of the three with 116 tracks.
		if strings.Contains(q, "tracks:") {
			t.Errorf("query %q filters on the index's track count", q)
		}
		if got := r.URL.Query().Get("limit"); got != "25" {
			t.Errorf("limit = %q, want 25: the matching edition may rank low", got)
		}
		respond(t, w, `{"releases": [{"id": "r1", "title": "The Divine Feminine", "track-count": 13}, {"id": "r2", "title": "The Divine Feminine", "track-count": 13},
			{"id": "other", "title": "The Divine Feminine", "track-count": 10}]}`)
	})
	mux.HandleFunc("/release/r1", func(w http.ResponseWriter, r *http.Request) {
		lookups.Add(1)
		if !strings.Contains(r.URL.Query().Get("inc"), "isrcs") {
			t.Errorf("lookup inc = %q, want isrcs among them", r.URL.Query().Get("inc"))
		}
		respond(t, w, `{"id": "r1", "title": "The Divine Feminine", "status": "Official", "date": "2026-10-02", "disambiguation": "10th Anniversary",
			"cover-art-archive": {"front": true},
			"media": [{"position": 1, "tracks": [{"number": "1", "position": 1, "title": "Congratulations", "recording": {"id": "rec-1", "isrcs": ["USWB11601443"]}}]}]}`)
	})
	mux.HandleFunc("/release/other", func(w http.ResponseWriter, r *http.Request) {
		t.Error("looked up a release with another track count")
	})
	mux.HandleFunc("/release/r2", func(w http.ResponseWriter, r *http.Request) {
		respond(t, w, `{"id": "r2", "title": "The Divine Feminine", "status": "Bootleg",
			"cover-art-archive": {"front": false},
			"media": [{"position": 1, "tracks": [{"number": "1", "position": 1, "title": "Congratulations", "recording": {"id": "rec-1"}}]}]}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := NewWithURL(srv.URL, "https://caa.test/release")
	c.api.Throttle = throttle.New(0)
	got, err := c.FindReleases(t.Context(), "The Divine Feminine", "Mac Miller", 13)
	if err != nil {
		t.Fatalf("FindReleases: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("got %d releases, want 2", len(got))
	}
	r1 := got[0]
	if r1.ID != "r1" || r1.Status != "Official" || r1.Date != "2026-10-02" || r1.Disambiguation != "10th Anniversary" ||
		r1.ArtworkURL != "https://caa.test/release/r1/front-500" {
		t.Errorf("r1 = %+v, want Official, 2026-10-02, 10th Anniversary, its front cover", r1)
	}
	if len(r1.Tracks) != 1 || r1.Tracks[0].ISRC != "USWB11601443" {
		t.Errorf("r1 tracks = %+v, want the recording's ISRC", r1.Tracks)
	}
	if got[1].ArtworkURL != "" {
		t.Errorf("r2 has no front cover, got artwork %q", got[1].ArtworkURL)
	}

	if _, err := c.LookupTracklist(t.Context(), "r1"); err != nil {
		t.Fatal(err)
	}
	if n := lookups.Load(); n != 1 {
		t.Errorf("release r1 fetched %d times, want 1: the lookups share the tracklist cache", n)
	}
}

// MusicBrainz often answers 503 under load: one failed lookup must not hide the other candidates.
func TestFindReleasesKeepsTheCandidatesThatLoaded(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/release", func(w http.ResponseWriter, r *http.Request) {
		respond(t, w, `{"releases": [{"id": "bad", "track-count": 1}, {"id": "good", "track-count": 1}]}`)
	})
	mux.HandleFunc("/release/bad", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "broken", http.StatusInternalServerError)
	})
	mux.HandleFunc("/release/good", func(w http.ResponseWriter, r *http.Request) {
		respond(t, w, `{"id": "good", "media": [{"position": 1, "tracks": [{"number": "1", "title": "A", "recording": {"id": "x"}}]}]}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	got, err := newTestClient(srv.URL).FindReleases(t.Context(), "Album", "Artist", 1)
	if err == nil {
		t.Error("expected the failed lookup to be reported")
	}
	if len(got) != 1 || got[0].ID != "good" {
		t.Errorf("got %+v, want only the release that loaded", got)
	}
}
