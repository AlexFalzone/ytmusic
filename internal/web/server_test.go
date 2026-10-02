package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The test runs from internal/web: an asset resolved against the working directory would 404.
func TestRouterServesLoginPageFromAnyWorkingDirectory(t *testing.T) {
	s := newTestServer(t, nil)

	for path, want := range map[string]string{
		"/login.html": "<form",
		"/login.js":   "fetch(",
		"/style.css":  "{",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		s.Router().ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("%s: got %d, want 200", path, rec.Code)
			continue
		}
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("%s: body does not look like the real asset (no %q)", path, want)
		}
	}
}
