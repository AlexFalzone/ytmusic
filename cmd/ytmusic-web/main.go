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
	"ytmusic/internal/web"
)

func main() {
	var (
		port       int
		configPath string
	)

	flag.IntVar(&port, "port", 8080, "HTTP server port")
	flag.StringVar(&configPath, "config", "", "Config file path")
	flag.Parse()

	// Load config or use defaults
	var cfg config.Config
	if configPath != "" {
		var err error
		cfg, err = config.LoadConfigFile(configPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Config error: %v\n", err)
			os.Exit(1)
		}
	} else {
		cfg = config.DefaultConfig()
	}

	// Setup logger with file logging
	l := logger.New(false)
	logDir := config.GetDefaultLogPath()
	if err := os.MkdirAll(logDir, 0755); err == nil {
		logPath := filepath.Join(logDir, fmt.Sprintf("ytmusic-web-%d.log", time.Now().Unix()))
		if err := l.SetFileLog(logPath); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Failed to setup file logging: %v\n", err)
		}
	}
	defer l.Close()

	// Create job manager and server
	jobMgr := web.NewJobManager()
	server := web.NewServer(jobMgr, cfg, l)

	// HTTP server
	httpServer := &http.Server{
		Addr:         fmt.Sprintf(":%d", port),
		Handler:      server.Router(),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start server in background
	go func() {
		l.Info("Starting web server on port %d", port)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			l.Error("Server error: %v", err)
			os.Exit(1)
		}
	}()

	// Graceful shutdown
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	l.Info("Shutting down server...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(ctx); err != nil {
		l.Error("Server shutdown error: %v", err)
	}

	l.Info("Server stopped")
}
