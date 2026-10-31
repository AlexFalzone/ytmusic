package fingerprint_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"ytmusic/internal/buildinfo"
	"ytmusic/internal/fingerprint"
	"ytmusic/internal/metadata"
	"ytmusic/internal/testhttp"
	"ytmusic/internal/throttle"
)

func TestAcoustIDClient_Lookup_Found(t *testing.T) {
	payload := map[string]any{
		"status": "ok",
		"results": []map[string]any{
			{
				"id":    "acoustid-1",
				"score": 0.95,
				"recordings": []map[string]any{
					{"id": "mbid-abc-123"},
				},
			},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		testhttp.JSON(t, w, payload)
	}))
	defer srv.Close()

	client := newTestClient("test-key", srv.URL)
	recs, err := client.Lookup(t.Context(), fingerprint.Result{Duration: 240, Fingerprint: "AQADtMm..."})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(recs) != 1 || recs[0].MBID != "mbid-abc-123" {
		t.Fatalf("recordings = %+v, want mbid-abc-123", recs)
	}
}

func TestAcoustIDClient_Lookup_NoResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		testhttp.JSON(t, w, map[string]any{"status": "ok", "results": []any{}})
	}))
	defer srv.Close()

	client := newTestClient("test-key", srv.URL)
	recs, err := client.Lookup(t.Context(), fingerprint.Result{Duration: 240, Fingerprint: "AQADtMm..."})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(recs) != 0 {
		t.Fatalf("recordings = %+v, want none", recs)
	}
}

func TestAcoustIDClient_Lookup_NoRecordings(t *testing.T) {
	payload := map[string]any{
		"status": "ok",
		"results": []map[string]any{
			{
				"id":         "acoustid-1",
				"score":      0.95,
				"recordings": []any{},
			},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		testhttp.JSON(t, w, payload)
	}))
	defer srv.Close()

	client := newTestClient("test-key", srv.URL)
	recs, err := client.Lookup(t.Context(), fingerprint.Result{Duration: 240, Fingerprint: "AQADtMm..."})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(recs) != 0 {
		t.Fatalf("recordings = %+v, want none for a result without recordings", recs)
	}
}

func TestAcoustIDClient_SpacesRequests(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		testhttp.JSON(t, w, map[string]any{"status": "ok", "results": []any{}})
	}))
	defer srv.Close()

	client := fingerprint.NewAcoustIDClient("test-key", srv.URL)
	start := time.Now()
	for range 3 {
		if _, err := client.Lookup(t.Context(), fingerprint.Result{Duration: 240, Fingerprint: "AQ"}); err != nil {
			t.Fatalf("Lookup: %v", err)
		}
	}
	if elapsed := time.Since(start); elapsed < 600*time.Millisecond {
		t.Errorf("three lookups took %v, want at least 600ms", elapsed)
	}
}

func TestAcoustIDClient_Lookup_IdentifiesItself(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); got != buildinfo.UserAgent() {
			t.Errorf("User-Agent = %q, want %q", got, buildinfo.UserAgent())
		}
		testhttp.JSON(t, w, map[string]any{"status": "ok", "results": []any{}})
	}))
	defer srv.Close()

	client := newTestClient("key", srv.URL)
	if _, err := client.Lookup(t.Context(), fingerprint.Result{Duration: 1, Fingerprint: "AQ"}); err != nil {
		t.Fatalf("Lookup: %v", err)
	}
}

// An error remembered as "no match" would stop the per-file phase from retrying.
func TestAcoustIDClient_Lookup_ErrorStatusIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		testhttp.JSON(t, w, map[string]any{"status": "error", "error": map[string]any{"code": 3, "message": "invalid fingerprint"}})
	}))
	defer srv.Close()

	client := newTestClient("key", srv.URL)
	_, err := client.Lookup(t.Context(), fingerprint.Result{Duration: 1, Fingerprint: "AQ"})
	if err == nil || !strings.Contains(err.Error(), "invalid fingerprint") {
		t.Errorf("err = %v, want one naming AcoustID's message", err)
	}
}

func TestAcoustIDClient_Lookup_HTTPErrorKeepsTheMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		testhttp.JSON(t, w, map[string]any{"status": "error", "error": map[string]any{"code": 3, "message": "invalid fingerprint"}})
	}))
	defer srv.Close()

	client := newTestClient("key", srv.URL)
	_, err := client.Lookup(t.Context(), fingerprint.Result{Duration: 1, Fingerprint: "AQ"})
	if err == nil || !strings.Contains(err.Error(), "invalid fingerprint") {
		t.Errorf("err = %v, want one naming AcoustID's message", err)
	}
}

func TestAcoustIDClient_Lookup_SendsTheKeyInTheBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.RawQuery != "" {
			t.Errorf("query = %q, want nothing in the URL", r.URL.RawQuery)
		}
		if got := r.PostFormValue("client"); got != "test-key" {
			t.Errorf("client = %q, want test-key", got)
		}
		testhttp.JSON(t, w, map[string]any{"status": "ok", "results": []any{}})
	}))
	defer srv.Close()

	client := newTestClient("test-key", srv.URL)
	if _, err := client.Lookup(t.Context(), fingerprint.Result{Duration: 1, Fingerprint: "AQ"}); err != nil {
		t.Fatalf("Lookup: %v", err)
	}
}

// A network error carries the request URL, and errors end up in logs.
func TestAcoustIDClient_Lookup_KeyStaysOutOfErrors(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()

	client := newTestClient("secret-key-123", srv.URL)
	_, err := client.Lookup(t.Context(), fingerprint.Result{Duration: 1, Fingerprint: "AQ"})
	if err == nil {
		t.Fatal("Lookup = nil, want the network error")
	}
	if strings.Contains(err.Error(), "secret-key-123") {
		t.Errorf("err = %q, want it without the key", err)
	}
}

// The limit is AcoustID's per address: two clients each spacing their own requests would together exceed it.
func TestAcoustIDClientsShareTheRateLimit(t *testing.T) {
	a := fingerprint.NewAcoustIDClient("key-a", "")
	b := fingerprint.NewAcoustIDClient("key-b", "http://acoustid.test")
	if fingerprint.ThrottleOf(a) != fingerprint.ThrottleOf(b) {
		t.Error("two clients have their own throttles, want the one shared limit")
	}
}

// Its own unthrottled limiter, so tests do not wait on each other through the shared one.
func newTestClient(key, url string) *fingerprint.AcoustIDClient {
	c := fingerprint.NewAcoustIDClient(key, url)
	fingerprint.SetThrottle(c, throttle.New(0))
	return c
}

func TestAcoustIDClient_Lookup_RejectedKey(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusOK} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			testhttp.JSON(t, w, map[string]any{"status": "error", "error": map[string]any{"code": 4, "message": "invalid API key"}})
		}))
		_, err := newTestClient("key", srv.URL).Lookup(t.Context(), fingerprint.Result{Duration: 1, Fingerprint: "AQ"})
		srv.Close()
		if !errors.Is(err, fingerprint.ErrKeyRejected) {
			t.Errorf("HTTP %d: err = %v, want ErrKeyRejected", status, err)
		}
	}
}

func TestAcoustIDClient_Lookup_OtherErrorsAreNotARejectedKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		testhttp.JSON(t, w, map[string]any{"status": "error", "error": map[string]any{"code": 3, "message": "invalid fingerprint"}})
	}))
	defer srv.Close()

	_, err := newTestClient("key", srv.URL).Lookup(t.Context(), fingerprint.Result{Duration: 1, Fingerprint: "AQ"})
	if err == nil || errors.Is(err, fingerprint.ErrKeyRejected) || !strings.Contains(err.Error(), "invalid fingerprint") {
		t.Errorf("err = %v, want AcoustID's message and not ErrKeyRejected", err)
	}
}

// Every recording of every result that counts, so the resolver can tell the right one from AcoustID's wrong links.
func TestAcoustIDClient_Lookup_ReturnsEveryRecording(t *testing.T) {
	payload := map[string]any{
		"status": "ok",
		"results": []map[string]any{
			{"score": 0.9, "recordings": []map[string]any{
				{"id": "rec-hyfr", "title": "HYFR", "duration": 206.6, "artists": []map[string]any{{"name": "Drake"}, {"name": "Lil Wayne"}},
					"releasegroups": []map[string]any{{"title": "Take Care", "type": "Album", "artists": []map[string]any{{"name": "Drake"}}}}},
				{"id": "rec-lwyd", "title": "Look What You've Done", "duration": 301.9, "artists": []map[string]any{{"name": "Drake"}},
					"releasegroups": []map[string]any{{"title": "Heart & Soul", "type": "Album", "secondarytypes": []string{"Compilation"}, "artists": []map[string]any{{"name": "Various Artists"}}}}},
			}},
			{"score": 0.4, "recordings": []map[string]any{{"id": "rec-weak", "title": "Other"}}},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.PostFormValue("meta"); got != "recordings releasegroups" {
			t.Errorf("meta = %q, want recordings and their release groups", got)
		}
		testhttp.JSON(t, w, payload)
	}))
	defer srv.Close()

	recs, err := newTestClient("key", srv.URL).Lookup(t.Context(), fingerprint.Result{Duration: 301, Fingerprint: "AQ"})
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	want := []metadata.Recording{
		{MBID: "rec-hyfr", Title: "HYFR", Artist: "Drake, Lil Wayne", Duration: 206600 * time.Millisecond,
			Albums: []metadata.RecordingAlbum{{Title: "Take Care", Artist: "Drake"}}},
		{MBID: "rec-lwyd", Title: "Look What You've Done", Artist: "Drake", Duration: 301900 * time.Millisecond,
			Albums: []metadata.RecordingAlbum{{Title: "Heart & Soul", Artist: "Various Artists", Secondary: true}}},
	}
	if !reflect.DeepEqual(recs, want) {
		t.Errorf("recordings = %+v\nwant %+v", recs, want)
	}
}
