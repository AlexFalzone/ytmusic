package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"ytmusic/internal/config"
	"ytmusic/internal/logger"
	"ytmusic/internal/pipeline"
	"ytmusic/internal/web"
)

// Stays under the 30 s the compose file gives a container to stop.
const (
	shutdownTimeout = 5 * time.Second
	jobsStopTimeout = 20 * time.Second
)

func main() {
	os.Exit(serve())
}

// A function of its own so the deferred calls run before the exit.
func serve() int {
	var (
		port         int
		configPath   string
		hashPassword bool
	)

	flag.IntVar(&port, "port", 8080, "HTTP server port")
	flag.StringVar(&configPath, "config", "", "Config file path")
	flag.BoolVar(&hashPassword, "hash-password", false, "Generate a bcrypt hash for auth.password_hash and exit")
	flag.Parse()

	// Before loading the config: it must work while the config still lacks the hash.
	if hashPassword {
		if err := runHashPassword(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return 1
		}
		return 0
	}

	cfg, err := config.LoadConfigFile(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Config error: %v\n", err)
		return 1
	}

	if err := cfg.ValidateWeb(); err != nil {
		fmt.Fprintf(os.Stderr, "Config error: %v\n", err)
		return 1
	}

	if err := pipeline.CheckTools(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Dependency error: %v\n", err)
		return 1
	}

	l := logger.New(false)
	if err := os.MkdirAll(cfg.LogDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Failed to create log directory: %v\n", err)
	} else {
		logPath := filepath.Join(cfg.LogDir, fmt.Sprintf("ytmusic-web-%d.log", time.Now().Unix()))
		if err := l.SetFileLog(logPath); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Failed to setup file logging: %v\n", err)
		}
	}
	defer func() {
		if err := l.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Failed to close log file: %v\n", err)
		}
	}()

	if !cfg.Auth.Enabled {
		l.Warn("authentication is DISABLED: anyone who can reach this server can control it")
	}

	signalled, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Not the signal's context: jobs stop only once no request can start another.
	ctx, cancelJobs := context.WithCancel(context.Background())
	defer cancelJobs()

	jobMgr := web.NewJobManager()
	jobMgr.StartCleanup(ctx)
	server := web.NewServer(ctx, jobMgr, cfg, l)
	server.StartSessionGC(ctx)

	httpServer := &http.Server{
		Addr:         fmt.Sprintf(":%d", port),
		Handler:      server.Router(),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	listenErr := make(chan error, 1)
	go func() {
		l.Info("Starting web server on port %d", port)
		listenErr <- httpServer.ListenAndServe()
	}()

	select {
	case err := <-listenErr:
		l.Error("Server error: %v", err)
		return 1
	case <-signalled.Done():
	}
	// A second signal kills the process, however the shutdown is going.
	stop()

	l.Info("Shutting down server...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer shutdownCancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		l.Error("Server shutdown error: %v", err)
	}

	cancelJobs()

	waited := make(chan struct{})
	go func() {
		server.Wait()
		close(waited)
	}()
	select {
	case <-waited:
	case <-time.After(jobsStopTimeout):
		l.Warn("timed out waiting for in-flight jobs to stop")
	}

	l.Info("Server stopped")
	return 0
}
