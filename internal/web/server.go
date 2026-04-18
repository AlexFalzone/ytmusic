package web

import (
	"context"
	"net/http"
	"strings"
	"time"

	"ytmusic/internal/config"
	"ytmusic/internal/logger"
)

type Server struct {
	ctx    context.Context
	jobMgr *JobManager
	config config.Config
	logger *logger.Logger
}

func NewServer(ctx context.Context, jobMgr *JobManager, cfg config.Config, log *logger.Logger) *Server {
	return &Server{
		ctx:    ctx,
		jobMgr: jobMgr,
		config: cfg,
		logger: log,
	}
}

func (s *Server) Router() http.Handler {
	mux := http.NewServeMux()

	// Static files
	mux.Handle("/", s.staticCacheMiddleware(http.FileServer(http.Dir("web/static"))))

	// API endpoints
	mux.HandleFunc("/api/download", s.handleDownload)
	mux.HandleFunc("/api/jobs", s.handleListJobs)
	mux.HandleFunc("/api/jobs/", s.handleJobAction)
	mux.HandleFunc("/ws", s.handleWebSocket)

	return s.loggingMiddleware(mux)
}

func (s *Server) staticCacheMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=3600")
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
