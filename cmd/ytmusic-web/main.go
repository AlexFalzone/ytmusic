package main

import (
	"context"
	"errors"
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

func main() {
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
			os.Exit(1)
		}
		return
	}

	cfg, err := config.LoadConfigFile(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Config error: %v\n", err)
		os.Exit(1)
	}

	if err := cfg.ValidateWeb(); err != nil {
		fmt.Fprintf(os.Stderr, "Config error: %v\n", err)
		os.Exit(1)
	}

	if err := pipeline.CheckTools(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Dependency error: %v\n", err)
		os.Exit(1)
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

	ctx, cancel := context.WithCancel(context.Background())

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

	go func() {
		l.Info("Starting web server on port %d", port)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			l.Error("Server error: %v", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	l.Info("Shutting down server...")
	cancel()

	waited := make(chan struct{})
	go func() {
		server.Wait()
		close(waited)
	}()
	select {
	case <-waited:
	case <-time.After(15 * time.Second):
		l.Warn("timed out waiting for in-flight jobs to stop")
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		l.Error("Server shutdown error: %v", err)
	}

	l.Info("Server stopped")
}
