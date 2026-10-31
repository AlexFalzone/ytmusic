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

func (h Hooks) warn(log *logger.Logger, msg string) {
	log.Warn("%s", msg)
	if h.OnWarning != nil {
		h.OnWarning(msg)
	}
}

func checkCancelled(ctx context.Context, stage string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("cancelled %s: %w", stage, err)
	}
	return nil
}

type dirImporter interface {
	Import(ctx context.Context, dir string, album *metadata.Album) error
}

type lyricsFetcher interface {
	Fetch(ctx context.Context, artist, title, album string) (lyrics.Result, error)
}

func Run(ctx context.Context, cfg config.Config, log *logger.Logger, tmpDir string, hooks Hooks) error {
	return run(ctx, cfg, log, tmpDir, hooks, importer.New(cfg, log), lyrics.NewClient(cfg.LyricsTimeout))
}

func run(ctx context.Context, cfg config.Config, log *logger.Logger, tmpDir string, hooks Hooks, imp dirImporter, lf lyricsFetcher) error {
	dl := downloader.New(cfg, log, tmpDir)
	dl.OnProgress = hooks.OnProgress

	pl, err := dl.Extract(ctx)
	if err != nil {
		return fmt.Errorf("failed to extract URLs: %w", err)
	}
	urls := make([]string, len(pl.Entries))
	for i, e := range pl.Entries {
		urls[i] = e.URL
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
	// DownloadAll notices a cancel only while starting downloads; one that comes later leaves them neither failed nor done.
	if err := checkCancelled(ctx, "while downloading"); err != nil {
		return err
	}

	if stats.Failed > 0 {
		hooks.warn(log, fmt.Sprintf("%d of %d videos failed to download (private, unavailable, or geo-restricted)", stats.Failed, stats.Total))
	}

	merged, err := dl.MergeFiles(stats.Produced())
	if err != nil {
		return fmt.Errorf("failed to merge files: %w", err)
	}
	for _, err := range merged.Failed {
		hooks.warn(log, fmt.Sprintf("file left out of the run: %v", err))
	}

	importErr := imp.Import(ctx, merged.Dir, albumFor(pl, stats.Files, merged.Moved))
	if err := checkCancelled(ctx, "while resolving metadata"); err != nil {
		return err
	}
	if importErr != nil {
		hooks.warn(log, fmt.Sprintf("metadata resolution failed: %v", importErr))
	}

	if !cfg.SkipLyrics {
		resolveLyrics(ctx, merged.Dir, log, lf)
		if err := checkCancelled(ctx, "while fetching lyrics"); err != nil {
			return err
		}
	}

	log.Info("moving files to %s", cfg.OutputDir)
	report, err := utils.MoveAudioFiles(merged.Dir, cfg.OutputDir, metadata.SubDirFromTags)
	if err != nil {
		return fmt.Errorf("failed to move files to output: %w", err)
	}
	for _, err := range report.Failed {
		hooks.warn(log, fmt.Sprintf("file not moved to the library: %v", err))
	}
	for _, err := range report.LyricsFailed {
		hooks.warn(log, fmt.Sprintf("lyrics not moved next to their track: %v", err))
	}
	log.Info("Moved %d files to %s", report.Moved, cfg.OutputDir)

	return nil
}

// nil unless the playlist is an album; each position follows its files through the download and the merge.
func albumFor(pl downloader.Playlist, files map[string][]string, mergedPaths map[string]string) *metadata.Album {
	title, ok := pl.Album()
	if !ok {
		return nil
	}

	count := pl.Count
	for _, e := range pl.Entries {
		count = max(count, e.Position)
	}
	album := &metadata.Album{Title: title, Tracks: make([]string, count), Files: make(map[string]int)}
	for _, e := range pl.Entries {
		if e.Position < 1 {
			continue
		}
		album.Tracks[e.Position-1] = e.Title
		for _, f := range files[e.URL] {
			if dst, ok := mergedPaths[f]; ok {
				album.Files[dst] = e.Position
			}
		}
	}
	return album
}

func RunImportOnly(ctx context.Context, cfg config.Config, log *logger.Logger, dir string) error {
	if err := importer.New(cfg, log).Import(ctx, dir, nil); err != nil {
		return fmt.Errorf("metadata resolution failed: %w", err)
	}

	if !cfg.SkipLyrics {
		resolveLyrics(ctx, dir, log, lyrics.NewClient(cfg.LyricsTimeout))
	}

	return nil
}

func ResolveLyrics(ctx context.Context, cfg config.Config, log *logger.Logger, dir string) {
	resolveLyrics(ctx, dir, log, lyrics.NewClient(cfg.LyricsTimeout))
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

		sem <- struct{}{}
		wg.Go(func() {
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
		})
	}

	wg.Wait()
}
