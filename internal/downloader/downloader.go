package downloader

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"ytmusic/internal/config"
	"ytmusic/internal/logger"
	"ytmusic/pkg/utils"
)

// Downloader handles downloading YouTube videos as audio files using yt-dlp
type Downloader struct {
	Config     config.Config
	Logger     *logger.Logger
	TmpDir     string
	OnProgress func() // Callback for progress updates
}

// New creates a new Downloader instance
func New(cfg config.Config, log *logger.Logger, tmpDir string) *Downloader {
	return &Downloader{
		Config: cfg,
		Logger: log,
		TmpDir: tmpDir,
	}
}

// ExtractURLs extracts individual video URLs from a playlist
func (d *Downloader) ExtractURLs(ctx context.Context) ([]string, error) {
	d.Logger.Info("=== Extracting URLs from playlist ===")
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
			return nil, fmt.Errorf("extraction cancelled")
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

// FetchMetadata fetches video metadata without downloading (for dry-run)
func (d *Downloader) FetchMetadata(ctx context.Context, urls []string) error {
	d.Logger.Info("=== Fetching video metadata (dry-run) ===")

	for i, url := range urls {
		select {
		case <-ctx.Done():
			return fmt.Errorf("metadata fetch cancelled")
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

// buildYtdlpArgs constructs command-line arguments for yt-dlp
func (d *Downloader) buildYtdlpArgs(url string) []string {
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
		"-o", outputTemplate,
		url,
	}

	// If empty yt-dlp will go to default (--no-cookies-from-browser)
	if d.Config.CookiesBrowser != "" {
		args = append(args, "--cookies-from-browser", d.Config.CookiesBrowser)
	}

	args = append(args, "-i", "-o", outputTemplate, url)

	return args
}

// DownloadSingle downloads a single video and converts it to audio
func (d *Downloader) DownloadSingle(ctx context.Context, url string) error {
	args := d.buildYtdlpArgs(url)
	cmd := exec.CommandContext(ctx, "yt-dlp", args...)

	if d.Config.Verbose {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}

	err := cmd.Run()
	if ctx.Err() != nil {
		return fmt.Errorf("download cancelled")
	}
	return err
}

// DownloadStats contains statistics about the download operation
type DownloadStats struct {
	Total      int
	Successful int
	Failed     int
}

// DownloadAll downloads all URLs in parallel using a worker pool
func (d *Downloader) DownloadAll(ctx context.Context, urls []string) (DownloadStats, error) {
	stats := DownloadStats{Total: len(urls)}

	if len(urls) == 0 {
		return stats, fmt.Errorf("no URLs to download")
	}

	d.Logger.Info("=== Starting download (%d videos, %d parallel) ===", len(urls), d.Config.ParallelJobs)

	var wg sync.WaitGroup
	semaphore := make(chan struct{}, d.Config.ParallelJobs)
	var failedMu sync.Mutex
	var failed []string

	for i, url := range urls {
		// Check if context is cancelled
		select {
		case <-ctx.Done():
			d.Logger.Warn("Downloads cancelled, waiting for active downloads to finish...")
			wg.Wait()
			stats.Failed = len(failed)
			stats.Successful = stats.Total - stats.Failed
			return stats, fmt.Errorf("downloads cancelled")
		default:
		}

		wg.Add(1)
		go func(idx int, u string) {
			defer wg.Done()

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

			// Call progress callback
			if d.OnProgress != nil {
				d.OnProgress()
			}
		}(i, url)
	}

	wg.Wait()

	// Calculate statistics
	stats.Failed = len(failed)
	stats.Successful = stats.Total - stats.Failed

	if len(failed) > 0 {
		d.Logger.Warn("⚠ %d videos not downloaded (private or unavailable)", len(failed))
		if d.Config.Verbose {
			d.Logger.Debug("Failed URLs: %v", failed)
		}

		// If ALL downloads failed, return an error
		if len(failed) == len(urls) {
			return stats, fmt.Errorf("all %d videos failed to download (private, unavailable, or geo-restricted)", len(urls))
		}
	}

	d.Logger.Info("Download completed: %d successful, %d failed", stats.Successful, stats.Failed)
	return stats, nil
}

// MergeFiles collects all MP3s into a single flat directory for beets import
func (d *Downloader) MergeFiles() (string, error) {
	d.Logger.Info("=== Merging MP3 files ===")

	mergedDir := filepath.Join(d.TmpDir, "merged")
	if err := os.MkdirAll(mergedDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create merged folder: %w", err)
	}

	files, err := utils.FindMP3Files(d.TmpDir)
	if err != nil {
		return "", fmt.Errorf("failed to search for MP3 files: %w", err)
	}

	d.Logger.Debug("Found %d MP3 files", len(files))

	if len(files) == 0 {
		return "", fmt.Errorf("no MP3 files found - all downloads may have failed")
	}

	var moveErrors int
	for _, file := range files {
		dst := filepath.Join(mergedDir, filepath.Base(file))
		if err := utils.MoveFile(file, dst); err != nil {
			d.Logger.Warn("Error moving %s: %v", file, err)
			moveErrors++
		}
	}

	if moveErrors > 0 {
		d.Logger.Warn("%d files could not be moved", moveErrors)
	}

	d.Logger.Info("MP3 files moved to: %s", mergedDir)
	return mergedDir, nil
}
