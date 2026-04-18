package fingerprint_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"ytmusic/internal/fingerprint"
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
		json.NewEncoder(w).Encode(payload)
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
		json.NewEncoder(w).Encode(map[string]any{"status": "ok", "results": []any{}})
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
		json.NewEncoder(w).Encode(payload)
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
