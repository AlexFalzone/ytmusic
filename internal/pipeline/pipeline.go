package pipeline

import (
	"context"
	"fmt"

	"ytmusic/internal/config"
	"ytmusic/internal/downloader"
	"ytmusic/internal/importer"
	"ytmusic/internal/logger"
	"ytmusic/internal/metadata"
	"ytmusic/internal/provider/spotify"
	"ytmusic/pkg/utils"
)

type Hooks struct {
	OnURLsExtracted func(total int)
	OnProgress      func()
	OnWarning       func(msg string)
}

// Run executes the full download pipeline: extract URLs → download → merge → resolve metadata → move.
func Run(ctx context.Context, cfg config.Config, log *logger.Logger, tmpDir string, hooks Hooks) error {
	dl := downloader.New(cfg, log, tmpDir)
	if hooks.OnProgress != nil {
		dl.OnProgress = hooks.OnProgress
	}

	urls, err := dl.ExtractURLs(ctx)
	if err != nil {
		return fmt.Errorf("failed to extract URLs: %w", err)
	}
	if len(urls) == 0 {
		return fmt.Errorf("no videos found in playlist - the playlist may be empty or private")
	}

	if hooks.OnURLsExtracted != nil {
		hooks.OnURLsExtracted(len(urls))
	}

	if cfg.DryRun {
		return dl.FetchMetadata(ctx, urls)
	}

	stats, err := dl.DownloadAll(ctx, urls)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}

	if stats.Failed > 0 {
		msg := fmt.Sprintf("%d of %d videos failed to download (private, unavailable, or geo-restricted)", stats.Failed, stats.Total)
		log.Warn(msg)
		if hooks.OnWarning != nil {
			hooks.OnWarning(msg)
		}
	}

	mergedDir, err := dl.MergeFiles()
	if err != nil {
		return fmt.Errorf("failed to merge files: %w", err)
	}

	provider := spotify.New(cfg.SpotifyClientID, cfg.SpotifyClientSecret)
	imp := importer.New(cfg, log, provider)
	if err := imp.Import(ctx, mergedDir); err != nil {
		msg := fmt.Sprintf("metadata resolution failed: %v", err)
		log.Warn(msg)
		if hooks.OnWarning != nil {
			hooks.OnWarning(msg)
		}
	}

	log.Info("=== Moving files to %s ===", cfg.OutputDir)
	moved, failed, err := utils.MoveAudioFiles(mergedDir, cfg.OutputDir, metadata.SubDirFromTags)
	if err != nil {
		return fmt.Errorf("failed to move files to output: %w", err)
	}
	if failed > 0 {
		log.Warn("%d files could not be moved", failed)
	}
	log.Info("Moved %d files to %s", moved, cfg.OutputDir)

	return nil
}
