package web

import (
	"context"
	"embed"
	"io/fs"
	"net/http"
	"net/url"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"ytmusic/internal/config"
	"ytmusic/internal/logger"
	"ytmusic/internal/pipeline"
)

//go:embed static
var staticFiles embed.FS

type Server struct {
	ctx      context.Context
	jobMgr   *JobManager
	config   config.Config
	logger   *logger.Logger
	sessions *sessionStore
	logins   *loginLimiter
	wg       sync.WaitGroup
	jobSem   chan struct{}

	runPipeline func(context.Context, config.Config, *logger.Logger, string, pipeline.Hooks) error
}

func (s *Server) Wait() {
	s.wg.Wait()
}

func NewServer(ctx context.Context, jobMgr *JobManager, cfg config.Config, log *logger.Logger) *Server {
	return &Server{
		ctx:      ctx,
		jobMgr:   jobMgr,
		config:   cfg,
		logger:   log,
		sessions: newSessionStore(cfg.Auth.TTL()),
		logins:   newLoginLimiter(),
		// An unbuffered channel would deadlock every job.
		jobSem:      make(chan struct{}, max(cfg.MaxConcurrentJobs, 1)),
		runPipeline: pipeline.Run,
	}
}

func (s *Server) Router() http.Handler {
	mux := http.NewServeMux()

	static, _ := fs.Sub(staticFiles, "static") // fails only on an invalid path
	mux.Handle("/", s.staticCacheMiddleware(http.FileServerFS(static)))

	mux.HandleFunc("/api/health", s.handleHealth)
	mux.HandleFunc("/api/login", s.handleLogin)
	mux.HandleFunc("/api/logout", s.handleLogout)
	mux.HandleFunc("/api/me", s.handleMe)
	mux.HandleFunc("/api/download", s.handleDownload)
	mux.HandleFunc("/api/jobs", s.handleListJobs)
	mux.HandleFunc("/api/jobs/", s.handleJobAction)
	mux.HandleFunc("/ws", s.handleWebSocket)

	// Outermost first: a cross-site request is rejected before it touches a session.
	return s.loggingMiddleware(s.requireSameOrigin(s.requireAuth(mux)))
}

func (s *Server) StartSessionGC(ctx context.Context) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				s.logger.Error("session GC panic: %v\n%s", r, debug.Stack())
			}
		}()

		ticker := time.NewTicker(sessionGCInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.sessions.gc()
				s.logins.gc()
			}
		}
	}()
}

const sessionGCInterval = 10 * time.Minute

// SameSite=Lax stops most cross-site requests; this closes the rest.
func (s *Server) requireSameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}

		if !isSameOrigin(r) {
			http.Error(w, "Cross-site request rejected", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isSameOrigin(r *http.Request) bool {
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" {
		return site == "same-origin" || site == "none"
	}
	return originMatchesHost(r)
}

// No Origin means a non-browser client: browsers always send it cross-origin.
func originMatchesHost(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

func (s *Server) staticCacheMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Never "public": a shared cache would serve one session's page to others.
		if r.URL.Path == "/" || strings.HasSuffix(r.URL.Path, ".html") {
			w.Header().Set("Cache-Control", "no-store")
		} else {
			w.Header().Set("Cache-Control", "private, max-age=3600")
		}
		next.ServeHTTP(w, r)
	})
}

type responseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriter) WriteHeader(status int) {
	rw.status = status
	rw.ResponseWriter.WriteHeader(status)
}

func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") || r.Header.Get("Upgrade") == "websocket" {
			next.ServeHTTP(w, r)
			return
		}

		rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(rw, r)
		s.logger.Info("%s %s %d (%s)", r.Method, r.URL.Path, rw.status, time.Since(start).Round(time.Millisecond))
	})
}
