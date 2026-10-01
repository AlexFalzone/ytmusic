package musicbrainz

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

const recordingJSON = `{
	"id": "rec-1",
	"title": "Song",
	"length": 200000,
	"artist-credit": [{"artist": {"id": "a1", "name": "Artist"}}],
	"releases": [
		{"id": "rel-1", "title": "Album", "status": "Official", "release-group": {"primary-type": "Album"}},
		{"id": "rel-2", "title": "Best Of", "status": "Official", "release-group": {"primary-type": "Album", "secondary-types": ["Compilation"]}}
	]
}`

const releaseJSON = `{
	"id": "rel-1",
	"title": "Album",
	"artist-credit": [{"artist": {"id": "a1", "name": "Artist"}}],
	"media": [{"position": 1, "tracks": [{"number": "1", "position": 1, "title": "Song", "recording": {"id": "rec-1"}}]}]
}`

// Phase A asks which releases hold a recording, and the fingerprint path then
// looks the same recording up for its metadata: one request serves both.
func TestRecordingIsFetchedOncePerRun(t *testing.T) {
	var lookups atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lookups.Add(1)
		w.Header().Set("Content-Type", "application/json")
		respond(t, w, recordingJSON)
	}))
	defer srv.Close()
	c := newTestClient(srv.URL)

	ids, err := c.ReleaseIDsForRecording(context.Background(), "rec-1")
	if err != nil || len(ids) != 2 || ids[0] != "rel-1" || ids[1] != "rel-2" {
		t.Fatalf("ReleaseIDsForRecording = %v, %v, want [rel-1 rel-2]", ids, err)
	}
	info, err := c.LookupByMBID(context.Background(), "rec-1", "")
	if err != nil || info.Title != "Song" || info.Album != "Album" {
		t.Fatalf("LookupByMBID = %+v, %v, want Song on Album", info, err)
	}

	if n := lookups.Load(); n != 1 {
		t.Errorf("recording requested %d times, want 1", n)
	}
}

// The dominant release of phase A is often the album phase B lands on.
func TestReleaseIsFetchedOncePerRun(t *testing.T) {
	var lookups atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/release/", func(w http.ResponseWriter, r *http.Request) {
		lookups.Add(1)
		w.Header().Set("Content-Type", "application/json")
		respond(t, w, releaseJSON)
	})
	mux.HandleFunc("/release", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		respond(t, w, `{"releases": [{"id": "rel-1", "title": "Album", "status": "Official"}]}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(srv.URL)

	if _, err := c.LookupTracklist(context.Background(), "rel-1"); err != nil {
		t.Fatalf("LookupTracklist: %v", err)
	}
	tl, found, err := c.ResolveAlbum(context.Background(), "Album", "Artist")
	if err != nil || !found || len(tl.Tracks) != 1 {
		t.Fatalf("ResolveAlbum = %+v, %v, %v, want the one-track release", tl, found, err)
	}

	if n := lookups.Load(); n != 1 {
		t.Errorf("release requested %d times, want 1", n)
	}
}

// A 503 during phase A must not cost the recording for the rest of the run.
func TestFailedLookupIsAskedAgain(t *testing.T) {
	var lookups atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if lookups.Add(1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		respond(t, w, recordingJSON)
	}))
	defer srv.Close()
	c := newTestClient(srv.URL)

	if _, err := c.ReleaseIDsForRecording(context.Background(), "rec-1"); err == nil {
		t.Fatal("first lookup: want the server error")
	}
	if _, err := c.LookupByMBID(context.Background(), "rec-1", ""); err != nil {
		t.Fatalf("second lookup: %v", err)
	}
	if n := lookups.Load(); n != 2 {
		t.Errorf("recording requested %d times, want 2", n)
	}
}
