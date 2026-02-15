package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"ytmusic/internal/config"
	"ytmusic/internal/logger"
	"ytmusic/internal/pipeline"
	"ytmusic/internal/progress"
	"ytmusic/internal/shutdown"
	"ytmusic/pkg/utils"
)

func main() {
	cfg, configPath, err := parseArgs()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] %v\n", err)
		os.Exit(1)
	}

	sh := shutdown.New()
	sh.Listen()
	defer sh.Wait()

	log := logger.New(cfg.Verbose)
	defer log.Close()

	if !cfg.Verbose {
		logDir := config.GetDefaultLogPath()
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
		pipeline.ResolveLyrics(sh.Context(), cfg.LyricsOnly, log)
		log.Info("=== Lyrics fetch completed ===")
		return
	}

	if cfg.ImportOnly != "" {
		if err := pipeline.RunImportOnly(sh.Context(), cfg, log, cfg.ImportOnly); err != nil {
			log.Error("%v", err)
			os.Exit(1)
		}
		log.Info("=== Import completed ===")
		return
	}

	if err := cfg.Validate(); err != nil {
		log.Error("Configuration error: %v", err)
		os.Exit(1)
	}

	if err := run(sh, cfg, log); err != nil {
		log.Error("%v", err)
		os.Exit(1)
	}
}

func run(sh *shutdown.Handler, cfg config.Config, log *logger.Logger) error {
	log.Debug("Checking dependencies...")
	if err := utils.CheckDependencies(); err != nil {
		return fmt.Errorf("dependency check failed: %w", err)
	}

	tmpDir, err := utils.CreateTempDir()
	if err != nil {
		return fmt.Errorf("error creating temporary folder: %w", err)
	}
	log.Debug("Temporary folder: %s", tmpDir)

	sh.AddCleanup(func() {
		log.Debug("Cleaning up...")
		if err := utils.Cleanup(tmpDir); err != nil {
			log.Warn("Error during cleanup: %v", err)
		}
	})

	var bar *progress.Bar
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
	}

	err = pipeline.Run(sh.Context(), cfg, log, tmpDir, hooks)

	if bar != nil {
		bar.Finish()
		log.SetProgressBar(false)
	}

	if err != nil {
		return err
	}

	log.Info("=== Process completed successfully ===")
	return nil
}
