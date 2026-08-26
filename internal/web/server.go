package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"ytmusic/internal/config"
	"ytmusic/internal/logger"
)

type Server struct {
	ctx      context.Context
	jobMgr   *JobManager
	config   config.Config
	logger   *logger.Logger
	sessions *sessionStore
	logins   *loginLimiter
}

func NewServer(ctx context.Context, jobMgr *JobManager, cfg config.Config, log *logger.Logger) *Server {
	return &Server{
		ctx:      ctx,
		jobMgr:   jobMgr,
		config:   cfg,
		logger:   log,
		sessions: newSessionStore(cfg.Auth.TTL()),
		logins:   newLoginLimiter(),
	}
}

func (s *Server) Router() http.Handler {
	mux := http.NewServeMux()

	// Static files
	mux.Handle("/", s.staticCacheMiddleware(http.FileServer(http.Dir("web/static"))))

	// Auth endpoints
	mux.HandleFunc("/api/health", s.handleHealth)
	mux.HandleFunc("/api/login", s.handleLogin)
	mux.HandleFunc("/api/logout", s.handleLogout)
	mux.HandleFunc("/api/me", s.handleMe)

	// API endpoints
	mux.HandleFunc("/api/download", s.handleDownload)
	mux.HandleFunc("/api/jobs", s.handleListJobs)
	mux.HandleFunc("/api/jobs/", s.handleJobAction)
	mux.HandleFunc("/ws", s.handleWebSocket)

	// Outermost first: a cross-site request is rejected before it can touch a
	// session, and every request is logged whatever its outcome.
	return s.loggingMiddleware(s.requireSameOrigin(s.requireAuth(mux)))
}

// StartSessionGC collects expired sessions and login records until ctx is done.
func (s *Server) StartSessionGC(ctx context.Context) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				s.logger.Error("session GC panic: %v", r)
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

// requireSameOrigin blocks cross-site state-changing requests. SameSite=Lax on
// the session cookie already stops most of them; this closes the rest, including
// clients that send no cookie at all.
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
	// Where the browser sends it, Sec-Fetch-Site settles the question.
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" {
		return site == "same-origin" || site == "none"
	}

	origin := r.Header.Get("Origin")
	if origin == "" {
		// No Origin means a non-browser client: browsers always send it on
		// cross-origin requests.
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
		// Never "public": behind a session that would let a shared cache serve
		// one user's page to somebody else.
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
