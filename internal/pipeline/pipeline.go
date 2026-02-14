package pipeline

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"ytmusic/internal/config"
	"ytmusic/internal/downloader"
	"ytmusic/internal/importer"
	"ytmusic/internal/logger"
	"ytmusic/internal/lyrics"
	"ytmusic/internal/metadata"
	"ytmusic/internal/provider/deezer"
	"ytmusic/internal/provider/musicbrainz"
	"ytmusic/internal/provider/spotify"
	"ytmusic/pkg/utils"

	"go.senan.xyz/taglib"
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

	provider := buildProviders(cfg, log)
	if provider != nil {
		imp := importer.New(cfg, log, provider)
		if err := imp.Import(ctx, mergedDir); err != nil {
			msg := fmt.Sprintf("metadata resolution failed: %v", err)
			log.Warn(msg)
			if hooks.OnWarning != nil {
				hooks.OnWarning(msg)
			}
		}
	} else {
		log.Info("No metadata providers configured, skipping metadata resolution")
	}

	resolveLyrics(ctx, mergedDir, log)

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

// buildProviders creates a metadata.Provider based on cfg.MetadataProviders.
// Returns nil if no providers are configured.
func buildProviders(cfg config.Config, log *logger.Logger) metadata.Provider {
	if len(cfg.MetadataProviders) == 0 {
		return nil
	}

	var providers []metadata.Provider
	for _, name := range cfg.MetadataProviders {
		switch name {
		case "spotify":
			providers = append(providers, spotify.New(cfg.SpotifyClientID, cfg.SpotifyClientSecret))
		case "musicbrainz":
			providers = append(providers, musicbrainz.New())
		case "deezer":
			providers = append(providers, deezer.New())
		}
	}

	if len(providers) == 0 {
		return nil
	}
	if len(providers) == 1 {
		return providers[0]
	}
	return metadata.NewChainProvider(providers, log)
}

// resolveLyrics fetches lyrics from LRCLib for each audio file in dir.
// Synced lyrics are saved as .lrc sidecar files; plain lyrics are embedded in tags.
// Best-effort: errors are logged but don't stop the pipeline.
func resolveLyrics(ctx context.Context, dir string, log *logger.Logger) {
	files, err := utils.FindAudioFiles(dir)
	if err != nil || len(files) == 0 {
		return
	}

	log.Info("=== Fetching lyrics for %d files ===", len(files))
	client := lyrics.NewClient()

	const workers = 3
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup

	for _, path := range files {
		select {
		case <-ctx.Done():
			break
		default:
		}

		tags, err := taglib.ReadTags(path)
		if err != nil {
			continue
		}

		title := firstTag(tags, taglib.Title)
		artist := firstTag(tags, taglib.Artist)
		album := firstTag(tags, taglib.Album)
		if title == "" || artist == "" {
			continue
		}

		wg.Add(1)
		sem <- struct{}{}
		go func(path, artist, title, album string) {
			defer wg.Done()
			defer func() { <-sem }()

			result, err := client.Fetch(ctx, artist, title, album)
			if err != nil {
				log.Debug("lyrics fetch failed for %q: %v", title, err)
				return
			}

			if result.Synced != "" {
				lrcPath := strings.TrimSuffix(path, filepath.Ext(path)) + ".lrc"
				if err := os.WriteFile(lrcPath, []byte(result.Synced), 0644); err != nil {
					log.Debug("failed to write .lrc file: %v", err)
				} else {
					log.Debug("saved synced lyrics: %s", filepath.Base(lrcPath))
				}
			} else if result.Plain != "" {
				if err := taglib.WriteTags(path, map[string][]string{
					taglib.Lyrics: {result.Plain},
				}, 0); err != nil {
					log.Debug("failed to write lyrics tag: %v", err)
				} else {
					log.Debug("embedded plain lyrics for %q", title)
				}
			} else {
				log.Debug("no lyrics found for %q", title)
			}
		}(path, artist, title, album)
	}

	wg.Wait()
}

func firstTag(tags map[string][]string, key string) string {
	if vals, ok := tags[key]; ok && len(vals) > 0 {
		return vals[0]
	}
	return ""
}
