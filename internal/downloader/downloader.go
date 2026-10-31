package downloader

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"time"

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

// A child still holding stderr would otherwise keep Wait from returning after a cancel.
const ytdlpWaitDelay = 5 * time.Second

func ytdlp(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "yt-dlp", args...)
	killGroupOnCancel(cmd)
	cmd.WaitDelay = ytdlpWaitDelay
	return cmd
}

func (d *Downloader) Extract(ctx context.Context) (Playlist, error) {
	d.Logger.Info("extracting urls from playlist")
	d.Logger.Debug("Playlist URL: %s", d.Config.PlaylistURL)

	args := slices.Concat([]string{"--flat-playlist", "--print", entryTemplate}, d.cookieArgs(), []string{"--", d.Config.PlaylistURL})
	cmd := ytdlp(ctx, args...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return Playlist{}, fmt.Errorf("extracting URLs: %w", ctx.Err())
		}
		return Playlist{}, fmt.Errorf("yt-dlp failed to extract URLs: %s", failureReason(stderr.String(), err))
	}

	pl, err := parsePlaylist(&stdout, d.Config.AllowedHosts)
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

		args := slices.Concat([]string{"--print", "%(title)s - %(artist)s - %(duration_string)s", "--no-download"}, d.cookieArgs(), []string{"--", url})
		cmd := ytdlp(ctx, args...)

		var stdout bytes.Buffer
		cmd.Stdout = &stdout

		if err := cmd.Run(); err != nil {
			d.Logger.Warn("[%d/%d] Failed to fetch metadata for %s", i+1, len(urls), url)
			continue
		}

		d.Logger.Info("[%d/%d] %s", i+1, len(urls), strings.TrimSpace(stdout.String()))
	}

	return nil
}

// Private playlists and videos need them from the first call on.
func (d *Downloader) cookieArgs() []string {
	if d.Config.CookiesBrowser == "" {
		return nil
	}
	return []string{"--cookies-from-browser", d.Config.CookiesBrowser}
}

func (d *Downloader) buildYtdlpArgs(url, producedList string) []string {
	// A folder per video, or two with one title share a file. 200 bytes leave room for yt-dlp's suffixes under the 255-byte limit.
	outputTemplate := filepath.Join(d.TmpDir, "%(id)s", "%(title).200B.%(ext)s")

	return slices.Concat([]string{
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
	}, d.cookieArgs(), []string{"--", url})
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
	cmd := ytdlp(ctx, args...)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if d.Config.Verbose {
		cmd.Stdout = os.Stdout
		cmd.Stderr = io.MultiWriter(os.Stderr, &stderr)
	}

	runErr := cmd.Run()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("downloading %s: %w", url, ctx.Err())
	}
	if runErr != nil {
		d.Logger.Debug("yt-dlp failed on %s:\n%s", url, stderr.String())
		return nil, errors.New(failureReason(stderr.String(), runErr))
	}

	report, err := os.ReadFile(listPath)
	if err != nil {
		return nil, fmt.Errorf("reading what yt-dlp produced for %s: %w", url, err)
	}
	produced, err := verifyProduced(url, string(report))
	if err != nil {
		return nil, errors.New(failureReason(stderr.String(), err))
	}
	return produced, nil
}

// yt-dlp's last ERROR line says why; the rest of stderr is warnings and progress.
func failureReason(stderr string, err error) string {
	for _, line := range slices.Backward(strings.Split(stderr, "\n")) {
		if reason, ok := strings.CutPrefix(strings.TrimSpace(line), "ERROR: "); ok {
			return reason
		}
	}
	return err.Error()
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

type Failure struct {
	URL    string
	Reason string
}

type DownloadStats struct {
	Total      int
	Successful int
	Failures   []Failure           // in playlist order
	Files      map[string][]string // URL → the files yt-dlp produced for it
}

// Sorted, so a name clash in the merge numbers the same file _2 on every run.
func (s DownloadStats) Produced() []string {
	all := slices.Concat(slices.Collect(maps.Values(s.Files))...)
	slices.Sort(all)
	return all
}

// YouTube refuses some requests at random: public videos have failed once and worked on the next try. A var so tests can shorten it.
var RetryDelay = 5 * time.Second

func (d *Downloader) DownloadAll(ctx context.Context, urls []string) (DownloadStats, error) {
	stats := DownloadStats{Total: len(urls), Files: make(map[string][]string)}

	if len(urls) == 0 {
		return stats, fmt.Errorf("no URLs to download")
	}

	d.Logger.Info("starting download (%d videos, %d parallel)", len(urls), d.Config.ParallelJobs)

	stats.Failures = d.downloadBatch(ctx, urls, stats.Files, d.OnProgress)
	if len(stats.Failures) > 0 && ctx.Err() == nil {
		d.Logger.Info("retrying %d failed downloads", len(stats.Failures))
		select {
		case <-ctx.Done():
		case <-time.After(RetryDelay):
		}
		// The retry already counted once as progress.
		stats.Failures = d.downloadBatch(ctx, failedURLs(stats.Failures), stats.Files, nil)
	}
	if err := ctx.Err(); err != nil {
		return stats, fmt.Errorf("downloading playlist: %w", err)
	}

	stats.Successful = stats.Total - len(stats.Failures)
	d.Logger.Info("Download completed: %d successful, %d failed", stats.Successful, len(stats.Failures))

	if len(stats.Failures) == len(urls) {
		return stats, fmt.Errorf("all %d videos failed to download: %s", len(urls), stats.Failures[0].Reason)
	}
	return stats, nil
}

func failedURLs(failures []Failure) []string {
	urls := make([]string, len(failures))
	for i, f := range failures {
		urls[i] = f.URL
	}
	return urls
}

// Successes land in files. A video a cancel cut short is no failure: the caller checks ctx.
func (d *Downloader) downloadBatch(ctx context.Context, urls []string, files map[string][]string, onProgress func()) []Failure {
	reasons := make([]string, len(urls))
	var wg sync.WaitGroup
	var mu sync.Mutex // guards files
	semaphore := make(chan struct{}, d.Config.ParallelJobs)

start:
	for i, url := range urls {
		select {
		case <-ctx.Done():
			d.Logger.Warn("Downloads cancelled, waiting for active downloads to finish...")
			break start
		case semaphore <- struct{}{}:
		}

		wg.Go(func() {
			defer func() { <-semaphore }()
			defer func() {
				if r := recover(); r != nil {
					d.Logger.Error("panic while downloading %s: %v\n%s", url, r, debug.Stack())
					reasons[i] = fmt.Sprintf("internal error: %v", r)
				}
			}()

			d.Logger.Debug("Downloading [%d/%d]: %s", i+1, len(urls), url)

			got, err := d.DownloadSingle(ctx, url)
			switch {
			case err == nil:
				mu.Lock()
				files[url] = got
				mu.Unlock()
			case ctx.Err() == nil:
				d.Logger.Debug("Download error %s: %v", url, err)
				reasons[i] = err.Error()
			}

			if onProgress != nil {
				onProgress()
			}
		})
	}
	wg.Wait()

	var failures []Failure
	for i, reason := range reasons {
		if reason != "" {
			failures = append(failures, Failure{URL: urls[i], Reason: reason})
		}
	}
	return failures
}

type Merged struct {
	Dir    string
	Moved  map[string]string // each file's path before the merge → its path after
	Failed []error
}

// files are those yt-dlp reported: a failed conversion leaves its output in the temp dir too.
func (d *Downloader) MergeFiles(files []string) (Merged, error) {
	d.Logger.Info("merging audio files")

	if len(files) == 0 {
		return Merged{}, fmt.Errorf("no audio files found - all downloads may have failed")
	}
	// Only for a debug count: a failed walk just leaves the line out.
	if all, err := utils.FindAudioFiles(d.TmpDir); err == nil && len(all) > len(files) {
		d.Logger.Debug("leaving out %d audio files yt-dlp did not report", len(all)-len(files))
	}

	merged := Merged{Dir: filepath.Join(d.TmpDir, "merged"), Moved: make(map[string]string, len(files))}
	if err := os.MkdirAll(merged.Dir, 0755); err != nil {
		return Merged{}, fmt.Errorf("failed to create merged folder: %w", err)
	}

	seen := make(map[string]bool)
	for _, file := range files {
		base := filepath.Base(file)
		name := base
		for n := 1; seen[name]; n++ {
			name = utils.NumberedName(base, n+1)
		}
		seen[name] = true
		dst := filepath.Join(merged.Dir, name)

		if err := utils.MoveFile(file, dst); err != nil {
			merged.Failed = append(merged.Failed, err)
			continue
		}
		merged.Moved[file] = dst
	}

	d.Logger.Info("Audio files moved to: %s", merged.Dir)
	return merged, nil
}
