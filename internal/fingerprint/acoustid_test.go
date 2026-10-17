package fingerprint_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ytmusic/internal/buildinfo"
	"ytmusic/internal/fingerprint"
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
	mbid, found, err := client.Lookup(context.Background(), fingerprint.Result{Duration: 240, Fingerprint: "AQADtMm..."})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found {
		t.Fatal("expected found=true")
	}
	if mbid != "mbid-abc-123" {
		t.Fatalf("expected mbid %q, got %q", "mbid-abc-123", mbid)
	}
}

func TestAcoustIDClient_Lookup_NoResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		testhttp.JSON(t, w, map[string]any{"status": "ok", "results": []any{}})
	}))
	defer srv.Close()

	client := newTestClient("test-key", srv.URL)
	_, found, err := client.Lookup(context.Background(), fingerprint.Result{Duration: 240, Fingerprint: "AQADtMm..."})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found {
		t.Fatal("expected found=false")
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
	_, found, err := client.Lookup(context.Background(), fingerprint.Result{Duration: 240, Fingerprint: "AQADtMm..."})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found {
		t.Fatal("expected found=false for result with no recordings")
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
		if _, _, err := client.Lookup(context.Background(), fingerprint.Result{Duration: 240, Fingerprint: "AQ"}); err != nil {
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
	if _, _, err := client.Lookup(context.Background(), fingerprint.Result{Duration: 1, Fingerprint: "AQ"}); err != nil {
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
	_, _, err := client.Lookup(context.Background(), fingerprint.Result{Duration: 1, Fingerprint: "AQ"})
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
	_, _, err := client.Lookup(context.Background(), fingerprint.Result{Duration: 1, Fingerprint: "AQ"})
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
	if _, _, err := client.Lookup(context.Background(), fingerprint.Result{Duration: 1, Fingerprint: "AQ"}); err != nil {
		t.Fatalf("Lookup: %v", err)
	}
}

// A network error carries the request URL, and errors end up in logs.
func TestAcoustIDClient_Lookup_KeyStaysOutOfErrors(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()

	client := newTestClient("secret-key-123", srv.URL)
	_, _, err := client.Lookup(context.Background(), fingerprint.Result{Duration: 1, Fingerprint: "AQ"})
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
		_, _, err := newTestClient("key", srv.URL).Lookup(context.Background(), fingerprint.Result{Duration: 1, Fingerprint: "AQ"})
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

	_, _, err := newTestClient("key", srv.URL).Lookup(context.Background(), fingerprint.Result{Duration: 1, Fingerprint: "AQ"})
	if err == nil || errors.Is(err, fingerprint.ErrKeyRejected) || !strings.Contains(err.Error(), "invalid fingerprint") {
		t.Errorf("err = %v, want AcoustID's message and not ErrKeyRejected", err)
	}
}
