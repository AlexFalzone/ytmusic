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
	OnDownloadsDone func()
	OnWarning       func(msg string)
}

// What a run that reached its end left out.
type Report struct {
	FailedDownloads int
	NotMoved        int
}

func (r Report) Incomplete() bool {
	return r.FailedDownloads > 0 || r.NotMoved > 0
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

func Run(ctx context.Context, cfg config.Config, log *logger.Logger, tmpDir string, hooks Hooks) (Report, error) {
	return run(ctx, cfg, log, tmpDir, hooks, importer.New(cfg, log), lyrics.NewClient(cfg.LyricsTimeout))
}

func run(ctx context.Context, cfg config.Config, log *logger.Logger, tmpDir string, hooks Hooks, imp dirImporter, lf lyricsFetcher) (Report, error) {
	var report Report
	dl := downloader.New(cfg, log, tmpDir)
	dl.OnProgress = hooks.OnProgress

	pl, err := dl.Extract(ctx)
	if err != nil {
		return report, fmt.Errorf("failed to extract URLs: %w", err)
	}
	urls := pl.URLs()
	if len(urls) == 0 {
		return report, fmt.Errorf("no videos found in playlist - the playlist may be empty or private")
	}

	if hooks.OnURLsExtracted != nil {
		hooks.OnURLsExtracted(len(urls))
	}

	if cfg.DryRun {
		return report, dl.FetchMetadata(ctx, urls)
	}

	stats, err := dl.DownloadAll(ctx, urls)
	if hooks.OnDownloadsDone != nil {
		hooks.OnDownloadsDone()
	}
	if err != nil {
		return report, fmt.Errorf("download failed: %w", err)
	}

	report.FailedDownloads = len(stats.Failures)
	for _, f := range stats.Failures {
		hooks.warn(log, fmt.Sprintf("video not downloaded: %s: %s", videoLabel(pl, f.URL), f.Reason))
	}

	merged, err := dl.MergeFiles(stats.Produced())
	if err != nil {
		return report, fmt.Errorf("failed to merge files: %w", err)
	}
	report.NotMoved = len(merged.Failed)
	for _, err := range merged.Failed {
		hooks.warn(log, fmt.Sprintf("file left out of the run: %v", err))
	}

	importErr := imp.Import(ctx, merged.Dir, albumFor(pl, stats.Files, merged.Moved))
	if err := checkCancelled(ctx, "while resolving metadata"); err != nil {
		return report, err
	}
	if importErr != nil {
		hooks.warn(log, fmt.Sprintf("metadata resolution failed: %v", importErr))
	}

	if !cfg.SkipLyrics {
		lyricsReport, err := resolveLyrics(ctx, merged.Dir, log, lf)
		if err := checkCancelled(ctx, "while fetching lyrics"); err != nil {
			return report, err
		}
		if err != nil {
			hooks.warn(log, fmt.Sprintf("lyrics not fetched: %v", err))
		}
		if lyricsReport.Failed > 0 {
			hooks.warn(log, lyricsReport.failureSummary())
		}
	}

	log.Info("moving files to %s", cfg.OutputDir)
	moved, err := utils.MoveAudioFiles(merged.Dir, cfg.OutputDir, metadata.SubDirFromTags)
	if err != nil {
		return report, fmt.Errorf("failed to move files to output: %w", err)
	}
	report.NotMoved += len(moved.Failed)
	for _, err := range moved.Failed {
		hooks.warn(log, fmt.Sprintf("file not moved to the library: %v", err))
	}
	for _, err := range moved.LyricsFailed {
		hooks.warn(log, fmt.Sprintf("lyrics not moved next to their track: %v", err))
	}
	log.Info("Moved %d files to %s", moved.Moved, cfg.OutputDir)

	return report, nil
}

func videoLabel(pl downloader.Playlist, url string) string {
	for _, e := range pl.Entries {
		if e.URL == url && e.Title != "" {
			return fmt.Sprintf("%s (%s)", e.Title, url)
		}
	}
	return url
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

	if cfg.SkipLyrics {
		return nil
	}
	return ResolveLyrics(ctx, cfg, log, dir)
}

func ResolveLyrics(ctx context.Context, cfg config.Config, log *logger.Logger, dir string) error {
	report, err := resolveLyrics(ctx, dir, log, lyrics.NewClient(cfg.LyricsTimeout))
	if err != nil {
		return err
	}
	if report.Failed > 0 {
		log.Warn("%s", report.failureSummary())
	}
	return checkCancelled(ctx, "while fetching lyrics")
}

// What became of each file; a cancel leaves the rest uncounted.
type LyricsReport struct {
	Synced   int
	Plain    int
	NotFound int
	Failed   int
	Skipped  int // already has lyrics, or nothing to search with
	lastErr  error
}

func (r LyricsReport) failureSummary() string {
	return fmt.Sprintf("lyrics failed for %d files, the last with: %v", r.Failed, r.lastErr)
}

func resolveLyrics(ctx context.Context, dir string, log *logger.Logger, lf lyricsFetcher) (LyricsReport, error) {
	var report LyricsReport
	files, err := utils.FindAudioFiles(dir)
	if err != nil {
		return report, fmt.Errorf("listing audio files: %w", err)
	}
	if len(files) == 0 {
		log.Info("no audio files in %s", dir)
		return report, nil
	}

	log.Info("fetching lyrics for %d files", len(files))

	const workers = 3
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	var mu sync.Mutex // guards report

	record := func(update func(*LyricsReport)) {
		mu.Lock()
		defer mu.Unlock()
		update(&report)
	}
	skip := func(r *LyricsReport) { r.Skipped++ }

	for _, path := range files {
		if ctx.Err() != nil {
			break
		}

		tags, err := taglib.ReadTags(path)
		if err != nil {
			log.Debug("reading tags of %s: %v", path, err)
			record(skip)
			continue
		}

		lrcPath := strings.TrimSuffix(path, filepath.Ext(path)) + ".lrc"
		if _, err := os.Stat(lrcPath); err == nil {
			log.Debug("lyrics already exist: %s", filepath.Base(lrcPath))
			record(skip)
			continue
		}

		title := metadata.FirstTag(tags, taglib.Title)
		artist := metadata.FirstTag(tags, taglib.Artist)
		album := metadata.FirstTag(tags, taglib.Album)
		if title == "" || artist == "" {
			log.Debug("no title or artist to search lyrics with: %s", filepath.Base(path))
			record(skip)
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
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				log.Debug("lyrics fetch failed for %q: %v", title, err)
				record(func(r *LyricsReport) { r.Failed++; r.lastErr = err })
				return
			}
			record(saveLyrics(result, path, lrcPath, title, log))
		})
	}

	wg.Wait()

	log.Info("lyrics: %d synced, %d plain, %d not found, %d failed, %d skipped",
		report.Synced, report.Plain, report.NotFound, report.Failed, report.Skipped)
	return report, nil
}

func saveLyrics(result lyrics.Result, path, lrcPath, title string, log *logger.Logger) func(*LyricsReport) {
	failed := func(err error) func(*LyricsReport) {
		log.Debug("saving lyrics of %q: %v", title, err)
		return func(r *LyricsReport) { r.Failed++; r.lastErr = err }
	}

	switch {
	case result.Synced != "":
		if err := os.WriteFile(lrcPath, []byte(result.Synced), 0644); err != nil {
			return failed(err)
		}
		log.Debug("saved synced lyrics: %s", filepath.Base(lrcPath))
		return func(r *LyricsReport) { r.Synced++ }
	case result.Plain != "":
		if err := taglib.WriteTags(path, map[string][]string{taglib.Lyrics: {result.Plain}}, 0); err != nil {
			return failed(err)
		}
		log.Debug("embedded plain lyrics for %q", title)
		return func(r *LyricsReport) { r.Plain++ }
	default:
		log.Debug("no lyrics found for %q", title)
		return func(r *LyricsReport) { r.NotFound++ }
	}
}
