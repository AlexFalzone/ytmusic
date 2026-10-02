package fingerprint_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ytmusic/internal/buildinfo"
	"ytmusic/internal/fingerprint"
	"ytmusic/internal/testhttp"
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

	client := fingerprint.NewAcoustIDClient("test-key", srv.URL)
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

	client := fingerprint.NewAcoustIDClient("test-key", srv.URL)
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

	client := fingerprint.NewAcoustIDClient("test-key", srv.URL)
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

	client := fingerprint.NewAcoustIDClient("key", srv.URL)
	if _, _, err := client.Lookup(context.Background(), fingerprint.Result{Duration: 1, Fingerprint: "AQ"}); err != nil {
		t.Fatalf("Lookup: %v", err)
	}
}

// An error remembered as "no match" would stop the per-file phase from retrying.
func TestAcoustIDClient_Lookup_ErrorStatusIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		testhttp.JSON(t, w, map[string]any{"status": "error", "error": map[string]any{"code": 4, "message": "invalid API key"}})
	}))
	defer srv.Close()

	client := fingerprint.NewAcoustIDClient("key", srv.URL)
	_, _, err := client.Lookup(context.Background(), fingerprint.Result{Duration: 1, Fingerprint: "AQ"})
	if err == nil || !strings.Contains(err.Error(), "invalid API key") {
		t.Errorf("err = %v, want one naming AcoustID's message", err)
	}
}

func TestAcoustIDClient_Lookup_HTTPErrorKeepsTheMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		testhttp.JSON(t, w, map[string]any{"status": "error", "error": map[string]any{"code": 3, "message": "invalid fingerprint"}})
	}))
	defer srv.Close()

	client := fingerprint.NewAcoustIDClient("key", srv.URL)
	_, _, err := client.Lookup(context.Background(), fingerprint.Result{Duration: 1, Fingerprint: "AQ"})
	if err == nil || !strings.Contains(err.Error(), "invalid fingerprint") {
		t.Errorf("err = %v, want one naming AcoustID's message", err)
	}
}
