package web

import (
	"context"
	"net/http"

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

func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.logger.Debug("%s %s", r.Method, r.URL.Path)
		next.ServeHTTP(w, r)
	})
}
