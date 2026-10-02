package downloader

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"

	"ytmusic/internal/config"
	"ytmusic/internal/logger"
	"ytmusic/pkg/utils"
)

type Downloader struct {
	Config     config.Config
	Logger     *logger.Logger
	TmpDir     string
	OnProgress func()
}

func New(cfg config.Config, log *logger.Logger, tmpDir string) *Downloader {
	return &Downloader{
		Config: cfg,
		Logger: log,
		TmpDir: tmpDir,
	}
}

func (d *Downloader) ExtractURLs(ctx context.Context) ([]string, error) {
	d.Logger.Info("extracting urls from playlist")
	d.Logger.Debug("Playlist URL: %s", d.Config.PlaylistURL)

	cmd := exec.CommandContext(ctx, "yt-dlp",
		"--flat-playlist",
		"--print", "https://www.youtube.com/watch?v=%(id)s",
		d.Config.PlaylistURL,
	)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("extracting URLs: %w", ctx.Err())
		}
		return nil, fmt.Errorf("yt-dlp failed to extract URLs: %w\nDetails: %s", err, stderr.String())
	}

	var urls []string
	scanner := bufio.NewScanner(&stdout)
	for scanner.Scan() {
		url := scanner.Text()
		if url != "" {
			urls = append(urls, url)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading yt-dlp output: %w", err)
	}

	d.Logger.Info("Found %d videos", len(urls))
	return urls, nil
}

func (d *Downloader) FetchMetadata(ctx context.Context, urls []string) error {
	d.Logger.Info("fetching video metadata (dry-run)")

	for i, url := range urls {
		select {
		case <-ctx.Done():
			return fmt.Errorf("fetching metadata: %w", ctx.Err())
		default:
		}

		cmd := exec.CommandContext(ctx, "yt-dlp",
			"--print", "%(title)s - %(artist)s - %(duration_string)s",
			"--no-download",
			url,
		)

		var stdout bytes.Buffer
		cmd.Stdout = &stdout

		if err := cmd.Run(); err != nil {
			d.Logger.Warn("[%d/%d] Failed to fetch metadata for %s", i+1, len(urls), url)
			continue
		}

		d.Logger.Info("[%d/%d] %s", i+1, len(urls), stdout.String())
	}

	return nil
}

func (d *Downloader) buildYtdlpArgs(url, producedList string) []string {
	outputTemplate := filepath.Join(d.TmpDir, "%(artist)s", "%(album)s", "%(title)s.%(ext)s")

	args := []string{
		"--extract-audio",
		"--audio-format", d.Config.AudioFormat,
		"-f", "bestaudio[ext=m4a]/bestaudio/best",
		"--retries", "10",
		"--fragment-retries", "10",
		"--concurrent-fragments", "1",
		"--write-thumbnail",
		"--embed-thumbnail",
		"--embed-metadata",
		"-i",
		// Not --print: it implies --quiet, silencing the progress verbose mode shows.
		"--print-to-file", "after_move:filepath", producedList,
		"-o", outputTemplate,
		url,
	}

	if d.Config.CookiesBrowser != "" {
		args = append(args, "--cookies-from-browser", d.Config.CookiesBrowser)
	}

	return args
}

func (d *Downloader) DownloadSingle(ctx context.Context, url string) error {
	list, err := os.CreateTemp(d.TmpDir, "produced-*.txt")
	if err != nil {
		return fmt.Errorf("creating the download report file for %s: %w", url, err)
	}
	listPath := list.Name()
	if err := list.Close(); err != nil {
		return fmt.Errorf("closing the download report file for %s: %w", url, err)
	}
	// Best effort: the temp dir is removed as a whole anyway.
	defer func() { _ = os.Remove(listPath) }()

	args := d.buildYtdlpArgs(url, listPath)
	cmd := exec.CommandContext(ctx, "yt-dlp", args...)

	var stderr bytes.Buffer
	if d.Config.Verbose {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	} else {
		cmd.Stderr = &stderr
	}

	runErr := cmd.Run()
	if ctx.Err() != nil {
		return fmt.Errorf("downloading %s: %w", url, ctx.Err())
	}
	if runErr != nil && stderr.Len() > 0 {
		return fmt.Errorf("yt-dlp error: %w\nDetails: %s", runErr, stderr.String())
	}
	if runErr != nil {
		return runErr
	}

	produced, err := os.ReadFile(listPath)
	if err != nil {
		return fmt.Errorf("reading what yt-dlp produced for %s: %w", url, err)
	}
	return verifyProduced(url, string(produced))
}

// Under --ignore-errors yt-dlp can exit 0 having downloaded nothing.
func verifyProduced(url, report string) error {
	var produced int
	for line := range strings.SplitSeq(report, "\n") {
		path := strings.TrimSpace(line)
		if path == "" {
			continue
		}
		if _, err := os.Stat(path); err != nil {
			return fmt.Errorf("yt-dlp reported %s for %s but the file is not there: %w", path, url, err)
		}
		produced++
	}

	if produced == 0 {
		return fmt.Errorf("yt-dlp downloaded nothing for %s (it exited without an error, likely skipped under --ignore-errors)", url)
	}
	return nil
}

type DownloadStats struct {
	Total      int
	Successful int
	Failed     int
}

func (d *Downloader) DownloadAll(ctx context.Context, urls []string) (DownloadStats, error) {
	stats := DownloadStats{Total: len(urls)}

	if len(urls) == 0 {
		return stats, fmt.Errorf("no URLs to download")
	}

	d.Logger.Info("starting download (%d videos, %d parallel)", len(urls), d.Config.ParallelJobs)

	var wg sync.WaitGroup
	semaphore := make(chan struct{}, d.Config.ParallelJobs)
	var failedMu sync.Mutex
	var failed []string

	for i, url := range urls {
		select {
		case <-ctx.Done():
			d.Logger.Warn("Downloads cancelled, waiting for active downloads to finish...")
			wg.Wait()
			stats.Failed = len(failed)
			stats.Successful = stats.Total - stats.Failed
			return stats, fmt.Errorf("downloading playlist: %w", ctx.Err())
		default:
		}

		wg.Add(1)
		go func(idx int, u string) {
			defer wg.Done()

			defer func() {
				if r := recover(); r != nil {
					d.Logger.Error("panic while downloading %s: %v\n%s", u, r, debug.Stack())
					failedMu.Lock()
					failed = append(failed, u)
					failedMu.Unlock()
				}
			}()

			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			d.Logger.Debug("Downloading [%d/%d]: %s", idx+1, len(urls), u)

			if err := d.DownloadSingle(ctx, u); err != nil {
				if ctx.Err() == nil {
					d.Logger.Debug("Download error %s: %v", u, err)
					failedMu.Lock()
					failed = append(failed, u)
					failedMu.Unlock()
				}
			}

			if d.OnProgress != nil {
				d.OnProgress()
			}
		}(i, url)
	}

	wg.Wait()

	stats.Failed = len(failed)
	stats.Successful = stats.Total - stats.Failed

	if len(failed) > 0 {
		d.Logger.Warn("⚠ %d videos not downloaded (private or unavailable)", len(failed))
		if d.Config.Verbose {
			d.Logger.Debug("Failed URLs: %v", failed)
		}

		if len(failed) == len(urls) {
			return stats, fmt.Errorf("all %d videos failed to download (private, unavailable, or geo-restricted)", len(urls))
		}
	}

	d.Logger.Info("Download completed: %d successful, %d failed", stats.Successful, stats.Failed)
	return stats, nil
}

func (d *Downloader) MergeFiles() (string, error) {
	d.Logger.Info("merging audio files")

	mergedDir := filepath.Join(d.TmpDir, "merged")
	if err := os.MkdirAll(mergedDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create merged folder: %w", err)
	}

	files, err := utils.FindAudioFiles(d.TmpDir)
	if err != nil {
		return "", fmt.Errorf("failed to search for audio files: %w", err)
	}

	d.Logger.Debug("Found %d audio files", len(files))

	if len(files) == 0 {
		return "", fmt.Errorf("no audio files found - all downloads may have failed")
	}

	var moveErrors int
	seen := make(map[string]bool)
	for _, file := range files {
		base := filepath.Base(file)
		name := base
		for n := 1; seen[name]; n++ {
			name = utils.NumberedName(base, n+1)
		}
		seen[name] = true
		dst := filepath.Join(mergedDir, name)

		if err := utils.MoveFile(file, dst); err != nil {
			d.Logger.Warn("Error moving %s: %v", file, err)
			moveErrors++
		}
	}

	if moveErrors > 0 {
		d.Logger.Warn("%d files could not be moved", moveErrors)
	}

	d.Logger.Info("Audio files moved to: %s", mergedDir)
	return mergedDir, nil
}
