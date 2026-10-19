package downloader

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"slices"
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

func (d *Downloader) Extract(ctx context.Context) (Playlist, error) {
	d.Logger.Info("extracting urls from playlist")
	d.Logger.Debug("Playlist URL: %s", d.Config.PlaylistURL)

	cmd := exec.CommandContext(ctx, "yt-dlp",
		"--flat-playlist",
		"--print", entryTemplate,
		d.Config.PlaylistURL,
	)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return Playlist{}, fmt.Errorf("extracting URLs: %w", ctx.Err())
		}
		return Playlist{}, fmt.Errorf("yt-dlp failed to extract URLs: %w\nDetails: %s", err, stderr.String())
	}

	pl, err := parsePlaylist(&stdout)
	if err != nil {
		return Playlist{}, err
	}

	d.Logger.Info("Found %d videos", len(pl.Entries))
	return pl, nil
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
		// The release date instead of the upload date; with neither release field the upload date stays.
		"--parse-metadata", "%(release_date>%Y-%m-%d,release_year|)s:(?P<meta_date>.+)",
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

// The paths are the files yt-dlp reports for url.
func (d *Downloader) DownloadSingle(ctx context.Context, url string) ([]string, error) {
	list, err := os.CreateTemp(d.TmpDir, "produced-*.txt")
	if err != nil {
		return nil, fmt.Errorf("creating the download report file for %s: %w", url, err)
	}
	listPath := list.Name()
	if err := list.Close(); err != nil {
		return nil, fmt.Errorf("closing the download report file for %s: %w", url, err)
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
		return nil, fmt.Errorf("downloading %s: %w", url, ctx.Err())
	}
	if runErr != nil && stderr.Len() > 0 {
		return nil, fmt.Errorf("yt-dlp error: %w\nDetails: %s", runErr, stderr.String())
	}
	if runErr != nil {
		return nil, runErr
	}

	report, err := os.ReadFile(listPath)
	if err != nil {
		return nil, fmt.Errorf("reading what yt-dlp produced for %s: %w", url, err)
	}
	return verifyProduced(url, string(report))
}

// Under --ignore-errors yt-dlp can exit 0 having downloaded nothing.
func verifyProduced(url, report string) ([]string, error) {
	var produced []string
	for line := range strings.SplitSeq(report, "\n") {
		path := strings.TrimSpace(line)
		if path == "" {
			continue
		}
		if _, err := os.Stat(path); err != nil {
			return nil, fmt.Errorf("yt-dlp reported %s for %s but the file is not there: %w", path, url, err)
		}
		produced = append(produced, path)
	}

	if len(produced) == 0 {
		return nil, fmt.Errorf("yt-dlp downloaded nothing for %s (it exited without an error, likely skipped under --ignore-errors)", url)
	}
	return produced, nil
}

type DownloadStats struct {
	Total      int
	Successful int
	Failed     int
	Files      map[string][]string // URL → the files yt-dlp produced for it
}

// Sorted, so a name clash in the merge numbers the same file _2 on every run.
func (s DownloadStats) Produced() []string {
	var all []string
	for _, files := range s.Files {
		all = append(all, files...)
	}
	slices.Sort(all)
	return all
}

func (d *Downloader) DownloadAll(ctx context.Context, urls []string) (DownloadStats, error) {
	stats := DownloadStats{Total: len(urls), Files: make(map[string][]string)}

	if len(urls) == 0 {
		return stats, fmt.Errorf("no URLs to download")
	}

	d.Logger.Info("starting download (%d videos, %d parallel)", len(urls), d.Config.ParallelJobs)

	var wg sync.WaitGroup
	semaphore := make(chan struct{}, d.Config.ParallelJobs)
	var mu sync.Mutex // guards failed and stats.Files
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
					mu.Lock()
					failed = append(failed, u)
					mu.Unlock()
				}
			}()

			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			d.Logger.Debug("Downloading [%d/%d]: %s", idx+1, len(urls), u)

			files, err := d.DownloadSingle(ctx, u)
			cancelled := ctx.Err() != nil
			if err != nil && !cancelled {
				d.Logger.Debug("Download error %s: %v", u, err)
			}
			mu.Lock()
			switch {
			case err == nil:
				stats.Files[u] = files
			case !cancelled:
				failed = append(failed, u)
			}
			mu.Unlock()

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

// files are those yt-dlp reported: a failed conversion leaves its output in the temp dir too.
// moved maps each file's path before the merge to its path after.
func (d *Downloader) MergeFiles(files []string) (dir string, moved map[string]string, err error) {
	d.Logger.Info("merging audio files")

	if len(files) == 0 {
		return "", nil, fmt.Errorf("no audio files found - all downloads may have failed")
	}
	if all, err := utils.FindAudioFiles(d.TmpDir); err == nil && len(all) > len(files) {
		d.Logger.Debug("leaving out %d audio files yt-dlp did not report", len(all)-len(files))
	}

	mergedDir := filepath.Join(d.TmpDir, "merged")
	if err := os.MkdirAll(mergedDir, 0755); err != nil {
		return "", nil, fmt.Errorf("failed to create merged folder: %w", err)
	}

	moved = make(map[string]string, len(files))
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
			continue
		}
		moved[file] = dst
	}

	if moveErrors > 0 {
		d.Logger.Warn("%d files could not be moved", moveErrors)
	}

	d.Logger.Info("Audio files moved to: %s", mergedDir)
	return mergedDir, moved, nil
}
