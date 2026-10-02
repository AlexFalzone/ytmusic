package pipeline

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"

	"ytmusic/internal/config"
	"ytmusic/internal/downloader"
	"ytmusic/internal/importer"
	"ytmusic/internal/logger"
	"ytmusic/internal/lyrics"
	"ytmusic/internal/metadata"
	"ytmusic/pkg/utils"

	"go.senan.xyz/taglib"
)

type Hooks struct {
	OnURLsExtracted func(total int)
	OnProgress      func()
	OnWarning       func(msg string)
}

type dirImporter interface {
	Import(ctx context.Context, dir string) error
}

type lyricsFetcher interface {
	Fetch(ctx context.Context, artist, title, album string) (lyrics.Result, error)
}

func Run(ctx context.Context, cfg config.Config, log *logger.Logger, tmpDir string, hooks Hooks) error {
	return run(ctx, cfg, log, tmpDir, hooks, importer.New(cfg, log), lyrics.NewClient())
}

func run(ctx context.Context, cfg config.Config, log *logger.Logger, tmpDir string, hooks Hooks, imp dirImporter, lf lyricsFetcher) error {
	dl := downloader.New(cfg, log, tmpDir)
	dl.OnProgress = hooks.OnProgress

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
		log.Warn("%s", msg)
		if hooks.OnWarning != nil {
			hooks.OnWarning(msg)
		}
	}

	mergedDir, err := dl.MergeFiles()
	if err != nil {
		return fmt.Errorf("failed to merge files: %w", err)
	}

	if err := imp.Import(ctx, mergedDir); err != nil {
		msg := fmt.Sprintf("metadata resolution failed: %v", err)
		log.Warn("%s", msg)
		if hooks.OnWarning != nil {
			hooks.OnWarning(msg)
		}
	}

	if !cfg.SkipLyrics {
		resolveLyrics(ctx, mergedDir, log, lf)
	}

	log.Info("moving files to %s", cfg.OutputDir)
	moved, failed, lyricsFailed, err := utils.MoveAudioFiles(mergedDir, cfg.OutputDir, metadata.SubDirFromTags)
	if err != nil {
		return fmt.Errorf("failed to move files to output: %w", err)
	}
	if failed > 0 {
		log.Warn("%d files could not be moved", failed)
	}
	if lyricsFailed > 0 {
		log.Warn("%d lyrics files could not be moved next to their track", lyricsFailed)
	}
	log.Info("Moved %d files to %s", moved, cfg.OutputDir)

	return nil
}

func RunImportOnly(ctx context.Context, cfg config.Config, log *logger.Logger, dir string) error {
	if err := importer.New(cfg, log).Import(ctx, dir); err != nil {
		return fmt.Errorf("metadata resolution failed: %w", err)
	}

	if !cfg.SkipLyrics {
		resolveLyrics(ctx, dir, log, lyrics.NewClient())
	}

	return nil
}

func ResolveLyrics(ctx context.Context, dir string, log *logger.Logger) {
	resolveLyrics(ctx, dir, log, lyrics.NewClient())
}

func resolveLyrics(ctx context.Context, dir string, log *logger.Logger, lf lyricsFetcher) {
	files, err := utils.FindAudioFiles(dir)
	if err != nil || len(files) == 0 {
		return
	}

	log.Info("fetching lyrics for %d files", len(files))

	const workers = 3
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup

	for _, path := range files {
		if ctx.Err() != nil {
			break
		}

		tags, err := taglib.ReadTags(path)
		if err != nil {
			continue
		}

		lrcPath := strings.TrimSuffix(path, filepath.Ext(path)) + ".lrc"
		if _, err := os.Stat(lrcPath); err == nil {
			log.Debug("lyrics already exist: %s", filepath.Base(lrcPath))
			continue
		}

		title := metadata.FirstTag(tags, taglib.Title)
		artist := metadata.FirstTag(tags, taglib.Artist)
		album := metadata.FirstTag(tags, taglib.Album)
		if title == "" || artist == "" {
			continue
		}

		wg.Add(1)
		sem <- struct{}{}
		go func(path, lrcPath, artist, title, album string) {
			defer wg.Done()
			defer func() { <-sem }()

			defer func() {
				if r := recover(); r != nil {
					log.Error("panic while fetching lyrics for %q: %v\n%s", title, r, debug.Stack())
				}
			}()

			result, err := lf.Fetch(ctx, artist, title, album)
			if err != nil {
				log.Debug("lyrics fetch failed for %q: %v", title, err)
				return
			}

			if result.Synced != "" {
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
		}(path, lrcPath, artist, title, album)
	}

	wg.Wait()
}
