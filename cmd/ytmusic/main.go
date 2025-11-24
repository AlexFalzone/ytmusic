package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"ytmusic/internal/config"
	"ytmusic/internal/downloader"
	"ytmusic/internal/importer"
	"ytmusic/internal/logger"
	"ytmusic/internal/progress"
	"ytmusic/internal/shutdown"
	"ytmusic/pkg/utils"
)

func main() {
	// Parse arguments and load config
	cfg, configPath, err := parseArgs()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] %v\n", err)
		os.Exit(1)
	}

	// Setup graceful shutdown
	sh := shutdown.New()
	sh.Listen()
	defer sh.Wait()

	// Initialize logger
	log := logger.New(cfg.Verbose)
	defer log.Close()

	// Setup file logging for non-verbose mode
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

	if err := cfg.Validate(); err != nil {
		log.Error("Configuration error: %v", err)
		os.Exit(1)
	}

	// Run main logic
	if err := run(sh, cfg, log); err != nil {
		log.Error("%v", err)
		os.Exit(1)
	}
}

// run executes the main program logic
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

	// Register cleanup
	sh.AddCleanup(func() {
		log.Debug("Cleaning up...")
		if err := utils.Cleanup(tmpDir); err != nil {
			log.Warn("Error during cleanup: %v", err)
		}
	})

	// Create downloader
	dl := downloader.New(cfg, log, tmpDir)

	// Extract URLs
	urls, err := dl.ExtractURLs(sh.Context())
	if err != nil {
		return fmt.Errorf("failed to extract URLs from playlist: %w", err)
	}

	if len(urls) == 0 {
		return fmt.Errorf("no videos found in playlist - the playlist may be empty or private")
	}

	// Dry-run mode: just show what would be downloaded
	if cfg.DryRun {
		log.Info("=== Dry-run mode: showing what would be downloaded ===")
		return dl.FetchMetadata(sh.Context(), urls)
	}

	// Setup progress bar for non-verbose mode
	var bar *progress.Bar
	if !cfg.Verbose {
		bar = progress.New(len(urls))
		log.SetProgressBar(true)
		dl.OnProgress = func() {
			bar.Increment()
		}
	}

	// Download all videos
	stats, err := dl.DownloadAll(sh.Context(), urls)
	if err != nil {
		if bar != nil {
			bar.Finish()
		}
		return fmt.Errorf("download failed: %w", err)
	}

	if bar != nil {
		bar.Finish()
		log.SetProgressBar(false)
	}

	// Report partial failures if any
	if stats.Failed > 0 {
		log.Warn("%d of %d videos failed to download (private, unavailable, or geo-restricted)", stats.Failed, stats.Total)
	}

	// Merge files
	mergedDir, err := dl.MergeFiles()
	if err != nil {
		return fmt.Errorf("failed to merge files: %w", err)
	}

	// Import to beets
	imp := importer.New(cfg, log)
	if err := imp.Import(sh.Context(), mergedDir); err != nil {
		return fmt.Errorf("beets import failed: %w", err)
	}

	log.Info("=== Process completed successfully ===")
	return nil
}
