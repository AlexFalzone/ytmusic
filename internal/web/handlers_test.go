package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ytmusic/internal/config"
	"ytmusic/internal/logger"
	"ytmusic/internal/pipeline"
)

func sessionCookie(t *testing.T, s *Server) *http.Cookie {
	t.Helper()
	token, err := s.sessions.create()
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	return &http.Cookie{Name: sessionCookieName, Value: token}
}

// One by one, not sampled: a single unprotected route defeats the feature.
func TestAllEndpointsRequireSession(t *testing.T) {
	s := newTestServer(t, nil)
	router := s.Router()

	endpoints := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/jobs"},
		{http.MethodGet, "/api/jobs/job_abc"},
		{http.MethodGet, "/api/jobs/job_abc/log"},
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

			if got := originMatchesHost(req); got != tt.want {
				t.Errorf("originMatchesHost() = %v, want %v", got, tt.want)
			}
		})
	}
}

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
			if err := s.jobMgr.UpdateJob(job.ID, func(j *Job) {
				j.Progress++
				j.Status = StatusRunning
			}); err != nil {
				t.Errorf("UpdateJob: %v", err)
			}
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
			s.runPipeline = func(context.Context, config.Config, *logger.Logger, string, pipeline.Hooks) error {
				return nil
			}

			body := `{"url":"` + tt.url + `"}`
			req := httptest.NewRequest(http.MethodPost, "/api/download", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.AddCookie(sessionCookie(t, s))

			rec := httptest.NewRecorder()
			s.Router().ServeHTTP(rec, req)

			if rec.Code != tt.wantCode {
				t.Errorf("got %d, want %d (body: %s)", rec.Code, tt.wantCode, rec.Body.String())
			}

			s.Wait()
		})
	}
}

type panickingReader struct{}

func (panickingReader) ReadMessage() (int, []byte, error) { panic("boom") }

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

func TestWaitBlocksUntilJobGoroutineFinishes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	s := newTestServer(t, nil)
	s.ctx = ctx

	// A cancelled context fails the pipeline before it reaches the network.
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

func TestCancelledJobEndsAsCancelled(t *testing.T) {
	s := newTestServer(t, nil)

	job, err := s.jobMgr.CreateJob(s.ctx, "https://youtube.com/playlist?list=x", s.config)
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	job.Cancel()

	s.wg.Add(1)
	s.processJob(job)

	final, err := s.jobMgr.GetJob(job.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if final.Status != StatusCancelled {
		t.Errorf("status = %q, want %q", final.Status, StatusCancelled)
	}
}

func TestJobCancelledByShutdownEndsAsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	s := newTestServer(t, nil)
	s.ctx = ctx

	job, err := s.jobMgr.CreateJob(s.ctx, "https://youtube.com/playlist?list=x", s.config)
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	cancel()

	s.wg.Add(1)
	s.processJob(job)

	final, err := s.jobMgr.GetJob(job.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if final.Status != StatusCancelled {
		t.Errorf("status = %q, want %q", final.Status, StatusCancelled)
	}
}

func startJob(t *testing.T, s *Server, url string) Job {
	t.Helper()

	cfg := s.config
	cfg.PlaylistURL = url
	job, err := s.jobMgr.CreateJob(s.ctx, url, cfg)
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	s.wg.Add(1)
	go s.processJob(job)
	return job
}

func TestJobsBeyondTheCapWaitTheirTurn(t *testing.T) {
	s := newTestServer(t, func(c *config.Config) { c.MaxConcurrentJobs = 1 })

	release := make(chan struct{})
	started := make(chan string, 2)
	s.runPipeline = func(ctx context.Context, cfg config.Config, log *logger.Logger, tmpDir string, hooks pipeline.Hooks) error {
		started <- cfg.PlaylistURL
		<-release
		return nil
	}

	startJob(t, s, "https://youtube.com/first")
	second := startJob(t, s, "https://youtube.com/second")

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("no job started at all")
	}

	select {
	case url := <-started:
		t.Fatalf("a second job started with the cap at 1: %s", url)
	case <-time.After(300 * time.Millisecond):
	}

	queued, err := s.jobMgr.GetJob(second.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if queued.Status != StatusPending && queued.Status != StatusRunning {
		t.Errorf("queued job status = %q, want pending", queued.Status)
	}

	close(release)

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the queued job never started once capacity freed")
	}

	s.Wait()
}

func TestJobCancelledWhileQueuedNeverStarts(t *testing.T) {
	s := newTestServer(t, func(c *config.Config) { c.MaxConcurrentJobs = 1 })

	release := make(chan struct{})
	defer close(release)

	var ran atomic.Int32
	blocking := make(chan struct{})
	s.runPipeline = func(ctx context.Context, cfg config.Config, log *logger.Logger, tmpDir string, hooks pipeline.Hooks) error {
		ran.Add(1)
		close(blocking)
		<-release
		return nil
	}

	startJob(t, s, "https://youtube.com/first")
	<-blocking // the first job holds the only slot

	queued := startJob(t, s, "https://youtube.com/queued")
	queued.Cancel()

	deadline := time.After(5 * time.Second)
	for {
		job, err := s.jobMgr.GetJob(queued.ID)
		if err != nil {
			t.Fatalf("GetJob: %v", err)
		}
		if job.Status == StatusCancelled {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("queued job status = %q, want cancelled", job.Status)
		case <-time.After(10 * time.Millisecond):
		}
	}

	if got := ran.Load(); got != 1 {
		t.Errorf("pipeline ran %d times, want 1: a cancelled job must never start", got)
	}
}

func TestJobCollectsEveryWarning(t *testing.T) {
	s := newTestServer(t, nil)
	s.runPipeline = func(ctx context.Context, cfg config.Config, log *logger.Logger, tmpDir string, hooks pipeline.Hooks) error {
		hooks.OnWarning("a")
		hooks.OnWarning("b")
		return nil
	}

	job := startJob(t, s, "https://youtube.com/x")
	s.Wait()

	got, err := s.jobMgr.GetJob(job.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got.Status != StatusCompleted || fmt.Sprint(got.Warnings) != "[a b]" || got.Error != "" {
		t.Errorf("job = %q, warnings %q, error %q; want completed, [a b], no error", got.Status, got.Warnings, got.Error)
	}
}

func TestJobLogHoldsThePipelinesLines(t *testing.T) {
	s := newTestServer(t, nil)
	s.runPipeline = func(ctx context.Context, cfg config.Config, log *logger.Logger, tmpDir string, hooks pipeline.Hooks) error {
		log.Debug("detail <&>")
		return nil
	}

	job := startJob(t, s, "https://youtube.com/x")
	s.Wait()

	lines, _, err := s.jobMgr.LogSince(job.ID, 0)
	if err != nil {
		t.Fatalf("LogSince: %v", err)
	}
	for _, l := range lines {
		if l.Level == "DEBUG" && l.Text == "detail <&>" && l.Prefix == "" {
			return
		}
	}
	t.Errorf("log = %+v, want the pipeline's debug line without the job's prefix", lines)
}

func postCancel(t *testing.T, s *Server, id string) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/jobs/"+id+"/cancel", nil)
	req.AddCookie(sessionCookie(t, s))
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	return rec.Code
}

func TestCancelLeavesTheFinalStateToTheJob(t *testing.T) {
	s := newTestServer(t, nil)
	running := make(chan struct{})
	release := make(chan struct{})
	// The run ends normally after the cancel, as when it arrives while files are being moved.
	s.runPipeline = func(ctx context.Context, cfg config.Config, log *logger.Logger, tmpDir string, hooks pipeline.Hooks) error {
		close(running)
		<-ctx.Done()
		<-release
		return nil
	}

	job := startJob(t, s, "https://youtube.com/x")
	<-running

	if code := postCancel(t, s, job.ID); code != http.StatusAccepted {
		t.Errorf("cancel = %d, want 202", code)
	}
	if got, _ := s.jobMgr.GetJob(job.ID); got.Status != StatusRunning {
		t.Errorf("status right after the cancel request = %q, want running: only the job writes its final state", got.Status)
	}

	close(release)
	s.Wait()

	if got, _ := s.jobMgr.GetJob(job.ID); got.Status != StatusCompleted {
		t.Errorf("final status = %q, want completed: the run finished", got.Status)
	}
}

func TestCancelOnAFinishedJobChangesNothing(t *testing.T) {
	s := newTestServer(t, nil)
	job, err := s.jobMgr.CreateJob(s.ctx, "https://youtube.com/x", s.config)
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	s.updateJob(job.ID, func(j *Job) { j.Status = StatusCompleted })

	for range 2 {
		if code := postCancel(t, s, job.ID); code != http.StatusAccepted {
			t.Errorf("cancel = %d, want 202", code)
		}
	}

	if got, _ := s.jobMgr.GetJob(job.ID); got.Status != StatusCompleted {
		t.Errorf("status = %q, want completed", got.Status)
	}
}

func TestJobLogEndpoint(t *testing.T) {
	s := newTestServer(t, nil)
	job, err := s.jobMgr.CreateJob(s.ctx, "https://youtube.com/x", s.config)
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	s.jobMgr.AppendLog(job.ID, logger.Line{Time: time.Now(), Level: "INFO", Text: "first"})
	s.jobMgr.AppendLog(job.ID, logger.Line{Time: time.Now(), Level: "WARN", Prefix: "[2/9]", Text: `"Rock & Roll" <live>`})

	get := func(id string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/jobs/"+id+"/log", nil)
		req.AddCookie(sessionCookie(t, s))
		rec := httptest.NewRecorder()
		s.Router().ServeHTTP(rec, req)
		return rec
	}

	rec := get(job.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET log = %d, want 200", rec.Code)
	}
	var resp LogResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if len(resp.Lines) != 2 || resp.Lines[1].Text != `"Rock & Roll" <live>` || resp.Lines[1].Prefix != "[2/9]" || resp.Dropped != 0 {
		t.Errorf("log = %+v, want both lines intact and nothing dropped", resp)
	}

	if rec := get("job_unknown"); rec.Code != http.StatusNotFound {
		t.Errorf("GET log of an unknown job = %d, want 404", rec.Code)
	}
}
