package downloader

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ytmusic/internal/config"
	"ytmusic/internal/logger"
)

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

	p1 := filepath.Join(tmpDir, "artist1", "album1", "song.mp3")
	p2 := filepath.Join(tmpDir, "artist2", "album2", "song.mp3")
	writeFile(t, p1, "content-1")
	writeFile(t, p2, "content-2")

	merged, err := d.MergeFiles([]string{p1, p2})
	if err != nil {
		t.Fatalf("MergeFiles() error: %v", err)
	}

	entries, err := os.ReadDir(merged.Dir)
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) != 2 {
		t.Fatalf("expected 2 files in merged dir, got %d", len(entries))
	}

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

	b1, _ := os.ReadFile(filepath.Join(merged.Dir, "song.mp3"))
	b2, _ := os.ReadFile(filepath.Join(merged.Dir, "song_2.mp3"))
	contents := map[string]bool{string(b1): true, string(b2): true}
	if !contents["content-1"] || !contents["content-2"] {
		t.Error("file contents were lost during merge")
	}
}

func TestMergeFilesTripleDuplicate(t *testing.T) {
	tmpDir := t.TempDir()
	log := logger.New(false)
	d := New(config.DefaultConfig(), log, tmpDir)

	var files []string
	for i := 1; i <= 3; i++ {
		path := filepath.Join(tmpDir, "artist", fmt.Sprintf("album%d", i), "track.mp3")
		writeFile(t, path, fmt.Sprintf("v%d", i))
		files = append(files, path)
	}

	merged, err := d.MergeFiles(files)
	if err != nil {
		t.Fatalf("MergeFiles() error: %v", err)
	}

	entries, err := os.ReadDir(merged.Dir)
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
	p1, p2 := filepath.Join(dir, "song1.mp3"), filepath.Join(dir, "song2.mp3")
	writeFile(t, p1, "a")
	writeFile(t, p2, "b")

	merged, err := d.MergeFiles([]string{p1, p2})
	if err != nil {
		t.Fatalf("MergeFiles() error: %v", err)
	}

	entries, err := os.ReadDir(merged.Dir)
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

	_, err := d.MergeFiles(nil)
	if err == nil {
		t.Error("MergeFiles() should fail with no audio files")
	}
}

// A conversion that fails leaves its output behind; only what yt-dlp reported belongs in the library.
func TestMergeFilesLeavesUndeclaredFilesOut(t *testing.T) {
	tmpDir := t.TempDir()
	d := New(config.DefaultConfig(), logger.New(false), tmpDir)

	dir := filepath.Join(tmpDir, "a", "b")
	song := filepath.Join(dir, "song.mp3")
	writeFile(t, song, "audio")
	writeFile(t, filepath.Join(dir, "partial.mp3"), "half")
	writeFile(t, filepath.Join(dir, "x.temp.m4a"), "")

	merged, err := d.MergeFiles([]string{song})
	if err != nil {
		t.Fatalf("MergeFiles() error: %v", err)
	}

	entries, err := os.ReadDir(merged.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "song.mp3" {
		t.Errorf("merged dir holds %v, want only song.mp3", entries)
	}
	if len(merged.Moved) != 1 {
		t.Errorf("moved = %v, want only the declared file", merged.Moved)
	}
}

func TestProducedListsEveryFileInOrder(t *testing.T) {
	stats := DownloadStats{Files: map[string][]string{
		"https://example.com/watch?v=b": {"/tmp/b.mp3"},
		"https://example.com/watch?v=a": {"/tmp/c.mp3", "/tmp/a.mp3"},
	}}

	if got := fmt.Sprint(stats.Produced()); got != "[/tmp/a.mp3 /tmp/b.mp3 /tmp/c.mp3]" {
		t.Errorf("Produced() = %s, want every file, sorted", got)
	}
}

func TestCancellationWrapsContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	cfg := config.DefaultConfig()
	cfg.PlaylistURL = "https://example.com/playlist"
	d := New(cfg, logger.New(false), t.TempDir())

	tests := []struct {
		name string
		call func() error
	}{
		{"Extract", func() error { _, err := d.Extract(ctx); return err }},
		{"FetchMetadata", func() error { return d.FetchMetadata(ctx, []string{"https://example.com/v"}) }},
		{"DownloadSingle", func() error { _, err := d.DownloadSingle(ctx, "https://example.com/v"); return err }},
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

// The progress hook is caller code running inside the worker: where a panic realistically comes from.
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
		_, _ = d.DownloadAll(t.Context(), []string{"not-a-url-1", "not-a-url-2", "not-a-url-3"})
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

// The stub exposes the --print-to-file target as $list, runs body and exits with code.
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
	// --ignore-errors: yt-dlp succeeds, the report stays empty.
	fakeYtdlp(t, ":", 0)

	d := New(config.DefaultConfig(), logger.New(false), t.TempDir())
	if _, err := d.DownloadSingle(t.Context(), "https://example.com/watch?v=x"); err == nil {
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
	got, err := d.DownloadSingle(t.Context(), "https://example.com/watch?v=x")
	if err != nil {
		t.Fatalf("DownloadSingle = %v, want nil", err)
	}
	if len(got) != 1 || got[0] != produced {
		t.Errorf("DownloadSingle reported %v, want [%s]", got, produced)
	}
}

func TestDownloadSingleFailsWhenThePrintedFileIsMissing(t *testing.T) {
	tmp := t.TempDir()
	fakeYtdlp(t, fmt.Sprintf(`printf '%%s\n' %q > "$list"`, filepath.Join(tmp, "gone.mp3")), 0)

	d := New(config.DefaultConfig(), logger.New(false), tmp)
	if _, err := d.DownloadSingle(t.Context(), "https://example.com/watch?v=x"); err == nil {
		t.Error("expected an error: the path yt-dlp printed does not exist")
	}
}

func TestDownloadAllReportsTheFilesOfEachURL(t *testing.T) {
	tmp := t.TempDir()
	fakeYtdlp(t, fmt.Sprintf(`for a in "$@"; do last="$a"; done
id="${last##*=}"
[ "$id" = dead ] && exit 1
printf x > %q/"$id".mp3
printf '%%s\n' %q/"$id".mp3 > "$list"`, tmp, tmp), 0)

	d := New(config.DefaultConfig(), logger.New(false), tmp)
	stats, err := d.DownloadAll(t.Context(), []string{"https://example.com/watch?v=a", "https://example.com/watch?v=dead"})
	if err != nil {
		t.Fatalf("DownloadAll: %v", err)
	}

	if got := stats.Files["https://example.com/watch?v=a"]; len(got) != 1 || got[0] != filepath.Join(tmp, "a.mp3") {
		t.Errorf("files of a = %v, want [%s]", got, filepath.Join(tmp, "a.mp3"))
	}
	if got, ok := stats.Files["https://example.com/watch?v=dead"]; ok {
		t.Errorf("a failed download reported files %v", got)
	}
}

func TestMergeFilesReportsAFileItCouldNotMove(t *testing.T) {
	tmp := t.TempDir()
	song := filepath.Join(tmp, "a", "song.mp3")
	if err := os.MkdirAll(filepath.Dir(song), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(song, []byte("audio"), 0644); err != nil {
		t.Fatal(err)
	}
	gone := filepath.Join(tmp, "b", "gone.mp3")

	d := New(config.Config{}, logger.New(false), tmp)
	merged, err := d.MergeFiles([]string{song, gone})
	if err != nil {
		t.Fatalf("MergeFiles() error: %v", err)
	}
	if len(merged.Failed) != 1 || !strings.Contains(merged.Failed[0].Error(), "gone.mp3") {
		t.Errorf("Failed = %v, want the file that was not there", merged.Failed)
	}
}

func TestMergeFilesReportsWhereEachFileWent(t *testing.T) {
	tmpDir := t.TempDir()
	d := New(config.DefaultConfig(), logger.New(false), tmpDir)

	src1 := filepath.Join(tmpDir, "artist1", "album1", "song.mp3")
	src2 := filepath.Join(tmpDir, "artist2", "album2", "song.mp3")
	writeFile(t, src1, "content-1")
	writeFile(t, src2, "content-2")

	merged, err := d.MergeFiles([]string{src1, src2})
	if err != nil {
		t.Fatalf("MergeFiles() error: %v", err)
	}

	for src, want := range map[string]string{src1: "content-1", src2: "content-2"} {
		dst, ok := merged.Moved[src]
		if !ok {
			t.Errorf("no destination reported for %s", src)
			continue
		}
		got, err := os.ReadFile(dst)
		if err != nil || string(got) != want {
			t.Errorf("%s went to %s holding %q (%v), want %q", src, dst, got, err, want)
		}
	}
}

func TestYtdlpOutputStaysApartAndFitsAName(t *testing.T) {
	tmp := t.TempDir()
	d := New(config.DefaultConfig(), logger.New(false), tmp)
	args := d.buildYtdlpArgs("https://example.com/watch?v=x", "list.txt")

	i := slices.Index(args, "-o")
	if i < 0 {
		t.Fatalf("args %q lack -o", args)
	}
	if want := filepath.Join(tmp, "%(id)s", "%(title).200B.%(ext)s"); args[i+1] != want {
		t.Errorf("output template = %q, want %q", args[i+1], want)
	}
}

func TestCookiesReachEveryYtdlpCall(t *testing.T) {
	bin := t.TempDir()
	log := filepath.Join(bin, "calls")
	script := "#!/bin/sh\necho \"$*\" >> \"" + log + "\"\n"
	if err := os.WriteFile(filepath.Join(bin, "yt-dlp"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	cfg := config.DefaultConfig()
	cfg.PlaylistURL = "https://example.com/private"
	cfg.CookiesBrowser = "firefox"
	d := New(cfg, logger.New(false), t.TempDir())

	if _, err := d.Extract(t.Context()); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if err := d.FetchMetadata(t.Context(), []string{"https://example.com/v"}); err != nil {
		t.Fatalf("FetchMetadata: %v", err)
	}

	calls, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(calls)), "\n")
	if len(lines) != 2 {
		t.Fatalf("yt-dlp ran %d times, want 2: %q", len(lines), lines)
	}
	for _, line := range lines {
		if !strings.Contains(line, "--cookies-from-browser firefox") {
			t.Errorf("no cookies in: %s", line)
		}
	}
}

// The date tag would otherwise hold the upload date, years after the release for most songs.
func TestYtdlpArgsWriteTheReleaseDate(t *testing.T) {
	d := New(config.DefaultConfig(), logger.New(false), t.TempDir())
	args := d.buildYtdlpArgs("https://example.com/watch?v=x", "list.txt")

	want := "%(release_date>%Y-%m-%d,release_year|)s:(?P<meta_date>.+)"
	for i, a := range args[:len(args)-1] {
		if a == "--parse-metadata" && args[i+1] == want {
			return
		}
	}
	t.Errorf("args %q lack --parse-metadata %q", args, want)
}
