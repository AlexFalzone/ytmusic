package web

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ytmusic/internal/logger"
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

func TestEveryResponseCarriesTheSecurityHeaders(t *testing.T) {
	s := newTestServer(t, nil)
	for _, path := range []string{"/login.html", "/api/health", "/api/jobs"} {
		w := httptest.NewRecorder()
		s.Router().ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))

		for header, want := range map[string]string{
			"X-Content-Type-Options": "nosniff",
			"X-Frame-Options":        "DENY",
			"Referrer-Policy":        "no-referrer",
		} {
			if got := w.Header().Get(header); got != want {
				t.Errorf("%s: %s = %q, want %q", path, header, got, want)
			}
		}
		if csp := w.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'self'") || !strings.Contains(csp, "frame-ancestors 'none'") {
			t.Errorf("%s: Content-Security-Policy = %q", path, csp)
		}
	}
}

func TestPagesRunNoInlineScript(t *testing.T) {
	for _, page := range []string{"static/index.html", "static/login.html"} {
		html, err := staticFiles.ReadFile(page)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(html, []byte("onclick=")) || bytes.Contains(html, []byte("<script>")) {
			t.Errorf("%s has an inline script, which the Content-Security-Policy blocks", page)
		}
	}
}

func TestHealthChecksStayOutOfTheLog(t *testing.T) {
	s := newTestServer(t, nil)
	var lines []string
	s.logger = logger.New(false).WithSink(func(l logger.Line) { lines = append(lines, l.Text) })

	s.Router().ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if len(lines) != 0 {
		t.Errorf("a health check logged %q", lines)
	}

	s.Router().ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/me", nil))
	if len(lines) != 1 {
		t.Errorf("logged %q, want the other API call", lines)
	}
}
