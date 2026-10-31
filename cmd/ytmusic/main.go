package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"ytmusic/internal/config"
	"ytmusic/internal/logger"
	"ytmusic/internal/pipeline"
	"ytmusic/internal/progress"
	"ytmusic/pkg/utils"
)

const exitIncomplete = 2

func main() {
	os.Exit(cli())
}

// A function of its own so the deferred calls run before the exit.
func cli() int {
	cfg, configPath, err := parseArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] %v\n", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// After the first signal the default action returns: a second Ctrl+C kills a run stuck on its way out.
	context.AfterFunc(ctx, stop)

	log := logger.New(cfg.Verbose)
	defer func() {
		if err := log.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "[WARN] Failed to close log file: %v\n", err)
		}
	}()

	if !cfg.Verbose {
		logDir := cfg.LogDir
		if err := os.MkdirAll(logDir, 0755); err != nil {
			fmt.Fprintf(os.Stderr, "[WARN] Failed to create log directory: %v\n", err)
		} else {
			logFile := filepath.Join(logDir, fmt.Sprintf("ytmusic_%s.log", time.Now().Format("2006-01-02_15-04-05")))
			if err := log.SetFileLog(logFile); err != nil {
				fmt.Fprintf(os.Stderr, "[WARN] Failed to setup file logging: %v\n", err)
			} else {
				log.Debug("Logging to file: %s", logFile)
			}
		}
	}

	if cfg.Verbose && configPath != "" {
		log.Debug("Loaded configuration from: %s", configPath)
	}

	if cfg.LyricsOnly != "" {
		if err := cfg.ValidateBase(); err != nil {
			log.Error("Configuration error: %v", err)
			return 1
		}
		if err := pipeline.ResolveLyrics(ctx, cfg, log, cfg.LyricsOnly); err != nil {
			log.Error("%v", err)
			return 1
		}
		log.Info("=== Lyrics fetch completed ===")
		return 0
	}

	if cfg.ImportOnly != "" {
		if err := cfg.ValidateBase(); err != nil {
			log.Error("Configuration error: %v", err)
			return 1
		}
		if err := pipeline.CheckImportTools(cfg); err != nil {
			log.Error("Dependency check failed: %v", err)
			return 1
		}
		if err := pipeline.RunImportOnly(ctx, cfg, log, cfg.ImportOnly); err != nil {
			log.Error("%v", err)
			return 1
		}
		log.Info("=== Import completed ===")
		return 0
	}

	if err := cfg.Validate(); err != nil {
		log.Error("Configuration error: %v", err)
		return 1
	}

	report, err := run(ctx, cfg, log)
	if err != nil {
		log.Error("%v", err)
		return 1
	}
	if report.Incomplete() {
		return exitIncomplete
	}
	return 0
}

func run(ctx context.Context, cfg config.Config, log *logger.Logger) (pipeline.Report, error) {
	log.Debug("Checking dependencies...")
	if err := pipeline.CheckTools(cfg); err != nil {
		return pipeline.Report{}, fmt.Errorf("dependency check failed: %w", err)
	}

	tmpDir, err := utils.CreateTempDir()
	if err != nil {
		return pipeline.Report{}, fmt.Errorf("error creating temporary folder: %w", err)
	}
	log.Debug("Temporary folder: %s", tmpDir)

	defer func() {
		if err := utils.Cleanup(tmpDir); err != nil {
			log.Warn("Error during cleanup: %v", err)
		}
	}()

	var bar *progress.Bar
	// With the bar gone, the rest of the run reaches the terminal.
	closeBar := func() {
		if bar != nil {
			bar.Finish()
			log.SetProgressBar(false)
		}
	}
	defer closeBar()

	warnings := 0
	hooks := pipeline.Hooks{
		OnURLsExtracted: func(total int) {
			if !cfg.Verbose && !cfg.DryRun {
				bar = progress.New(total)
				log.SetProgressBar(true)
			}
		},
		OnProgress: func() {
			if bar != nil {
				bar.Increment()
			}
		},
		OnDownloadsDone: closeBar,
		OnWarning:       func(string) { warnings++ },
	}

	report, err := pipeline.Run(ctx, cfg, log, tmpDir, hooks)
	if err != nil {
		return report, err
	}

	if warnings > 0 {
		log.Warn("=== Process completed with %d warnings ===", warnings)
	} else {
		log.Info("=== Process completed successfully ===")
	}
	return report, nil
}
