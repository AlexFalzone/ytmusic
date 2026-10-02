package testhttp

import (
	"encoding/json"
	"net/http"
	"testing"
)

// Errorf, not Fatal: it runs on the handler's goroutine.
func JSON(t testing.TB, w http.ResponseWriter, v any) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Errorf("encoding fake response: %v", err)
	}
}
