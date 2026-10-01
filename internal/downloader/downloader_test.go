package downloader

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"ytmusic/internal/config"
	"ytmusic/internal/logger"
)

// writeFile creates path, parent directories included, holding content.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestMergeFilesDeduplicate(t *testing.T) {
	tmpDir := t.TempDir()
	log := logger.New(false)
	d := New(config.DefaultConfig(), log, tmpDir)

	// Create two subdirectories with files that have the same name
	dir1 := filepath.Join(tmpDir, "artist1", "album1")
	dir2 := filepath.Join(tmpDir, "artist2", "album2")
	writeFile(t, filepath.Join(dir1, "song.mp3"), "content-1")
	writeFile(t, filepath.Join(dir2, "song.mp3"), "content-2")

	mergedDir, err := d.MergeFiles()
	if err != nil {
		t.Fatalf("MergeFiles() error: %v", err)
	}

	entries, err := os.ReadDir(mergedDir)
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) != 2 {
		t.Fatalf("expected 2 files in merged dir, got %d", len(entries))
	}

	// Verify both files exist with distinct names and original content
	names := make(map[string]bool)
	for _, e := range entries {
		names[e.Name()] = true
	}

	if !names["song.mp3"] {
		t.Error("expected song.mp3 in merged dir")
	}
	if !names["song_2.mp3"] {
		t.Error("expected song_2.mp3 in merged dir")
	}

	// Verify content is preserved (no data loss)
	b1, _ := os.ReadFile(filepath.Join(mergedDir, "song.mp3"))
	b2, _ := os.ReadFile(filepath.Join(mergedDir, "song_2.mp3"))
	contents := map[string]bool{string(b1): true, string(b2): true}
	if !contents["content-1"] || !contents["content-2"] {
		t.Error("file contents were lost during merge")
	}
}

func TestMergeFilesTripleDuplicate(t *testing.T) {
	tmpDir := t.TempDir()
	log := logger.New(false)
	d := New(config.DefaultConfig(), log, tmpDir)

	for i := 1; i <= 3; i++ {
		dir := filepath.Join(tmpDir, "artist", fmt.Sprintf("album%d", i))
		writeFile(t, filepath.Join(dir, "track.mp3"), fmt.Sprintf("v%d", i))
	}

	mergedDir, err := d.MergeFiles()
	if err != nil {
		t.Fatalf("MergeFiles() error: %v", err)
	}

	entries, err := os.ReadDir(mergedDir)
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) != 3 {
		t.Fatalf("expected 3 files, got %d", len(entries))
	}
}

func TestMergeFilesNoDuplicates(t *testing.T) {
	tmpDir := t.TempDir()
	log := logger.New(false)
	d := New(config.DefaultConfig(), log, tmpDir)

	dir := filepath.Join(tmpDir, "artist", "album")
	writeFile(t, filepath.Join(dir, "song1.mp3"), "a")
	writeFile(t, filepath.Join(dir, "song2.mp3"), "b")

	mergedDir, err := d.MergeFiles()
	if err != nil {
		t.Fatalf("MergeFiles() error: %v", err)
	}

	entries, err := os.ReadDir(mergedDir)
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) != 2 {
		t.Fatalf("expected 2 files, got %d", len(entries))
	}
}

func TestMergeFilesEmpty(t *testing.T) {
	tmpDir := t.TempDir()
	log := logger.New(false)
	d := New(config.DefaultConfig(), log, tmpDir)

	_, err := d.MergeFiles()
	if err == nil {
		t.Error("MergeFiles() should fail with no audio files")
	}
}

// Cancellation must keep its identity all the way up the stack. Matching on the
// error text instead would be fragile and is forbidden by the project rules.
func TestCancellationWrapsContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cfg := config.DefaultConfig()
	cfg.PlaylistURL = "https://example.com/playlist"
	d := New(cfg, logger.New(false), t.TempDir())

	tests := []struct {
		name string
		call func() error
	}{
		{"ExtractURLs", func() error { _, err := d.ExtractURLs(ctx); return err }},
		{"FetchMetadata", func() error { return d.FetchMetadata(ctx, []string{"https://example.com/v"}) }},
		{"DownloadSingle", func() error { return d.DownloadSingle(ctx, "https://example.com/v") }},
		{"DownloadAll", func() error { _, err := d.DownloadAll(ctx, []string{"https://example.com/v"}); return err }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.call()
			if err == nil {
				t.Fatal("expected an error from a cancelled context")
			}
			if !errors.Is(err, context.Canceled) {
				t.Errorf("error must wrap context.Canceled for errors.Is, got: %v", err)
			}
		})
	}
}

// The download workers run in their own goroutines, off any handler stack: an
// unhandled panic there takes down the whole process. The progress hook is
// supplied by the caller and runs inside the worker, so it is where a panic
// realistically comes from.
func TestDownloadAllSurvivesPanicInProgressHook(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ParallelJobs = 2
	d := New(cfg, logger.New(false), t.TempDir())

	var calls atomic.Int32
	d.OnProgress = func() {
		if calls.Add(1) == 1 {
			panic("boom")
		}
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = d.DownloadAll(context.Background(), []string{"not-a-url-1", "not-a-url-2", "not-a-url-3"})
	}()

	select {
	case <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("DownloadAll never returned after a worker panicked")
	}

	if got := calls.Load(); got != 3 {
		t.Errorf("progress hook ran %d times, want 3: a panic in one worker must not stop the others", got)
	}
}

// fakeYtdlp puts a stub yt-dlp first in PATH. The script finds the file that
// --print-to-file was pointed at, exposes it as $list, then runs body and exits
// with code.
func fakeYtdlp(t *testing.T, body string, code int) {
	t.Helper()
	dir := t.TempDir()
	script := fmt.Sprintf(`#!/bin/sh
next=0
list=""
for a in "$@"; do
	if [ "$next" = "1" ]; then list="$a"; next=0; fi
	if [ "$a" = "after_move:filepath" ]; then next=1; fi
done
%s
exit %d
`, body, code)
	path := filepath.Join(dir, "yt-dlp")
	if err := os.WriteFile(path, []byte(script), 0755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestDownloadSingleFailsWhenNothingWasProduced(t *testing.T) {
	// --ignore-errors lets yt-dlp succeed while producing no file at all.
	// The stub leaves the report file empty, as yt-dlp does when it skips.
	fakeYtdlp(t, ":", 0)

	d := New(config.DefaultConfig(), logger.New(false), t.TempDir())
	if err := d.DownloadSingle(context.Background(), "https://example.com/watch?v=x"); err == nil {
		t.Error("expected an error: yt-dlp exited 0 without downloading anything")
	}
}

func TestDownloadSingleAcceptsAnExistingFile(t *testing.T) {
	tmp := t.TempDir()
	produced := filepath.Join(tmp, "song.mp3")
	if err := os.WriteFile(produced, []byte("audio"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	fakeYtdlp(t, fmt.Sprintf(`printf '%%s\n' %q > "$list"`, produced), 0)

	d := New(config.DefaultConfig(), logger.New(false), tmp)
	if err := d.DownloadSingle(context.Background(), "https://example.com/watch?v=x"); err != nil {
		t.Errorf("DownloadSingle = %v, want nil", err)
	}
}

func TestDownloadSingleFailsWhenThePrintedFileIsMissing(t *testing.T) {
	tmp := t.TempDir()
	fakeYtdlp(t, fmt.Sprintf(`printf '%%s\n' %q > "$list"`, filepath.Join(tmp, "gone.mp3")), 0)

	d := New(config.DefaultConfig(), logger.New(false), tmp)
	if err := d.DownloadSingle(context.Background(), "https://example.com/watch?v=x"); err == nil {
		t.Error("expected an error: the path yt-dlp printed does not exist")
	}
}
