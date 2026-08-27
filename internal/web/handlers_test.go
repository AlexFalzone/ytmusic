package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ytmusic/internal/config"
)

// sessionCookie returns a cookie for a freshly created session.
func sessionCookie(t *testing.T, s *Server) *http.Cookie {
	t.Helper()
	token, err := s.sessions.create()
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	return &http.Cookie{Name: sessionCookieName, Value: token}
}

// Every endpoint must be closed by default. Checked one by one rather than by
// sampling: a single unprotected route defeats the whole feature.
func TestAllEndpointsRequireSession(t *testing.T) {
	s := newTestServer(t, nil)
	router := s.Router()

	endpoints := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/jobs"},
		{http.MethodGet, "/api/jobs/job_abc"},
		{http.MethodPost, "/api/jobs/job_abc/cancel"},
		{http.MethodPost, "/api/download"},
		{http.MethodGet, "/api/me"},
		{http.MethodPost, "/api/logout"},
		{http.MethodGet, "/ws"},
	}

	for _, e := range endpoints {
		t.Run(e.method+" "+e.path, func(t *testing.T) {
			req := httptest.NewRequest(e.method, e.path, strings.NewReader("{}"))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Errorf("got %d, want 401 without a session", rec.Code)
			}
		})
	}
}

func TestHealthIsPublic(t *testing.T) {
	s := newTestServer(t, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("got %d, want 200: the Docker healthcheck cannot authenticate", rec.Code)
	}
}

func TestListJobsWorksWithSession(t *testing.T) {
	s := newTestServer(t, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/jobs", nil)
	req.AddCookie(sessionCookie(t, s))
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("got %d, want 200 with a valid session", rec.Code)
	}
}

func TestDownloadRejectsNonJSONContentType(t *testing.T) {
	s := newTestServer(t, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/download",
		strings.NewReader(`{"url":"https://youtube.com/playlist?list=x"}`))
	req.Header.Set("Content-Type", "text/plain")
	req.AddCookie(sessionCookie(t, s))

	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("got %d, want 415: text/plain makes a cross-origin POST a simple request", rec.Code)
	}
}

func TestStateChangingRequestsCheckOrigin(t *testing.T) {
	tests := []struct {
		name      string
		origin    string
		fetchSite string
		wantCode  int
	}{
		{name: "no origin (non-browser client)", wantCode: http.StatusOK},
		{name: "same origin", origin: "http://example.com", wantCode: http.StatusOK},
		{name: "foreign origin", origin: "http://evil.test", wantCode: http.StatusForbidden},
		{name: "sec-fetch-site cross-site", fetchSite: "cross-site", wantCode: http.StatusForbidden},
		{name: "sec-fetch-site same-origin", fetchSite: "same-origin", wantCode: http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestServer(t, nil)

			req := httptest.NewRequest(http.MethodPost, "/api/logout", nil)
			req.Host = "example.com"
			req.Header.Set("Content-Type", "application/json")
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			if tt.fetchSite != "" {
				req.Header.Set("Sec-Fetch-Site", tt.fetchSite)
			}
			req.AddCookie(sessionCookie(t, s))

			rec := httptest.NewRecorder()
			s.Router().ServeHTTP(rec, req)

			if rec.Code != tt.wantCode {
				t.Errorf("got %d, want %d (body: %s)", rec.Code, tt.wantCode, rec.Body.String())
			}
		})
	}
}

func TestWebSocketOriginCheck(t *testing.T) {
	tests := []struct {
		name   string
		origin string
		want   bool
	}{
		{"no origin", "", true},
		{"same origin", "http://example.com", true},
		{"same origin https", "https://example.com", true},
		{"foreign origin", "http://evil.test", false},
		{"lookalike origin", "http://example.com.evil.test", false},
		{"malformed origin", "::::", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/ws", nil)
			req.Host = "example.com"
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}

			if got := checkWSOrigin(req); got != tt.want {
				t.Errorf("checkWSOrigin() = %v, want %v", got, tt.want)
			}
		})
	}
}

// A shared cache must never be allowed to store a page served behind a session.
func TestAuthenticatedResponsesAreNotPubliclyCacheable(t *testing.T) {
	s := newTestServer(t, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/jobs", nil)
	req.AddCookie(sessionCookie(t, s))
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)

	if strings.Contains(rec.Header().Get("Cache-Control"), "public") {
		t.Errorf("Cache-Control must not be public behind a session, got: %q", rec.Header().Get("Cache-Control"))
	}
}

// A job listing must be readable while the job is being updated: the manager
// hands out data, not pointers into its own mutable state.
func TestJobReadsDoNotRaceWithUpdates(t *testing.T) {
	s := newTestServer(t, nil)
	job, err := s.jobMgr.CreateJob(context.Background(), "https://example.com/playlist", s.config)
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			s.jobMgr.UpdateJob(job.ID, func(j *Job) {
				j.Progress++
				j.Status = StatusRunning
			})
		}
	}()

	for i := 0; i < 200; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/jobs", nil)
		req.AddCookie(sessionCookie(t, s))
		rec := httptest.NewRecorder()
		s.Router().ServeHTTP(rec, req)
	}
	<-done
}

func TestDownloadHostAllowlist(t *testing.T) {
	tests := []struct {
		name     string
		allowed  []string
		url      string
		wantCode int
	}{
		{name: "empty allowlist accepts anything", url: "https://vimeo.com/x", wantCode: http.StatusOK},
		{name: "exact host", allowed: []string{"youtube.com"}, url: "https://youtube.com/playlist?list=x", wantCode: http.StatusOK},
		{name: "subdomain", allowed: []string{"youtube.com"}, url: "https://www.youtube.com/playlist?list=x", wantCode: http.StatusOK},
		{name: "second entry", allowed: []string{"youtube.com", "vimeo.com"}, url: "https://vimeo.com/x", wantCode: http.StatusOK},
		{name: "foreign host", allowed: []string{"youtube.com"}, url: "https://evil.test/x", wantCode: http.StatusBadRequest},
		{name: "lookalike host", allowed: []string{"youtube.com"}, url: "https://notyoutube.com/x", wantCode: http.StatusBadRequest},
		{name: "suffix trick", allowed: []string{"youtube.com"}, url: "https://youtube.com.evil.test/x", wantCode: http.StatusBadRequest},
		{name: "unparsable url", allowed: []string{"youtube.com"}, url: "http://%zz", wantCode: http.StatusBadRequest},
		{name: "no host", allowed: nil, url: "https://", wantCode: http.StatusBadRequest},
		{name: "non-http scheme", allowed: nil, url: "file:///etc/passwd", wantCode: http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestServer(t, func(c *config.Config) { c.AllowedHosts = tt.allowed })

			body := `{"url":"` + tt.url + `"}`
			req := httptest.NewRequest(http.MethodPost, "/api/download", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.AddCookie(sessionCookie(t, s))

			rec := httptest.NewRecorder()
			s.Router().ServeHTTP(rec, req)

			if rec.Code != tt.wantCode {
				t.Errorf("got %d, want %d (body: %s)", rec.Code, tt.wantCode, rec.Body.String())
			}

			// Cancel any job an accepted request started, so the test does not
			// leave yt-dlp running in the background.
			if rec.Code == http.StatusOK {
				for _, j := range s.jobMgr.ListJobs(0) {
					if j.Cancel != nil {
						j.Cancel()
					}
				}
			}
		})
	}
}

type panickingReader struct{}

func (panickingReader) ReadMessage() (int, []byte, error) { panic("boom") }

// The read pump runs in its own goroutine, off any handler stack: an unhandled
// panic there takes down the whole server, every running job with it.
func TestReadPumpSurvivesPanic(t *testing.T) {
	s := newTestServer(t, nil)

	done := make(chan struct{})
	go s.readPump(panickingReader{}, done)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("read pump never signalled completion after a panic")
	}
}

// Shutdown must not cut a running job off mid-flight: Wait returns only once
// the job goroutine has finished.
func TestWaitBlocksUntilJobGoroutineFinishes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	s := newTestServer(t, nil)
	s.ctx = ctx

	// A cancelled context makes the pipeline fail immediately, without
	// reaching the network.
	cancel()

	req := httptest.NewRequest(http.MethodPost, "/api/download",
		strings.NewReader(`{"url":"https://youtube.com/playlist?list=x"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(sessionCookie(t, s))

	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("download request: got %d, want 200", rec.Code)
	}

	waited := make(chan struct{})
	go func() {
		s.Wait()
		close(waited)
	}()

	select {
	case <-waited:
	case <-time.After(10 * time.Second):
		t.Fatal("Wait did not return: a job goroutine is unaccounted for")
	}

	jobs := s.jobMgr.ListJobs(0)
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(jobs))
	}
	switch jobs[0].Status {
	case StatusCompleted, StatusFailed, StatusCancelled:
	default:
		t.Errorf("Wait returned while the job was still %q", jobs[0].Status)
	}
}
