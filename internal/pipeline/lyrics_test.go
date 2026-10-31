package pipeline

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"ytmusic/internal/logger"
	"ytmusic/internal/lyrics"
	"ytmusic/internal/metadata"
	"ytmusic/internal/testaudio"

	"go.senan.xyz/taglib"
)

type fakeLyrics struct {
	byTitle map[string]lyrics.Result
	errFor  map[string]error
	mu      sync.Mutex
	asked   []string
}

func (f *fakeLyrics) Fetch(_ context.Context, _, title, _ string) (lyrics.Result, error) {
	f.mu.Lock()
	f.asked = append(f.asked, title)
	f.mu.Unlock()
	return f.byTitle[title], f.errFor[title]
}

func trackFile(t *testing.T, dir, name string, tags map[string][]string) string {
	t.Helper()
	p := testaudio.MP3(t, dir, name, "0.1")
	if err := taglib.WriteTags(p, tags, 0); err != nil {
		t.Fatalf("tag %s: %v", p, err)
	}
	return p
}

func TestResolveLyrics(t *testing.T) {
	dir := t.TempDir()
	trackFile(t, dir, "synced.mp3", map[string][]string{taglib.Title: {"Synced"}, taglib.Artist: {"A"}})
	plain := trackFile(t, dir, "plain.mp3", map[string][]string{taglib.Title: {"Plain"}, taglib.Artist: {"A"}})
	trackFile(t, dir, "done.mp3", map[string][]string{taglib.Title: {"Done"}, taglib.Artist: {"A"}})
	trackFile(t, dir, "lonely.mp3", map[string][]string{taglib.Title: {"Lonely"}})
	doneLRC := filepath.Join(dir, "done.lrc")
	if err := os.WriteFile(doneLRC, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	f := &fakeLyrics{byTitle: map[string]lyrics.Result{
		"Synced": {Synced: "[00:01.00]la", Plain: "la"},
		"Plain":  {Plain: "words"},
		"Done":   {Synced: "[00:01.00]new"},
	}}
	if _, err := resolveLyrics(t.Context(), dir, logger.New(false), f); err != nil {
		t.Fatalf("resolveLyrics: %v", err)
	}

	if got, err := os.ReadFile(filepath.Join(dir, "synced.lrc")); err != nil || string(got) != "[00:01.00]la" {
		t.Errorf("synced.lrc = %q, %v; want the synced lyrics", got, err)
	}
	tags, err := taglib.ReadTags(plain)
	if err != nil {
		t.Fatal(err)
	}
	if got := metadata.FirstTag(tags, taglib.Lyrics); got != "words" {
		t.Errorf("plain lyrics tag = %q, want %q", got, "words")
	}
	if got, err := os.ReadFile(doneLRC); err != nil || string(got) != "old" {
		t.Errorf("done.lrc = %q, %v; want it left alone", got, err)
	}
	if slices.Contains(f.asked, "Done") || slices.Contains(f.asked, "Lonely") {
		t.Errorf("asked for %v: a track with lyrics already, or without an artist, needs no lookup", f.asked)
	}
}

func TestResolveLyricsCountsWhatBecameOfEachFile(t *testing.T) {
	dir := t.TempDir()
	tags := func(title string) map[string][]string {
		return map[string][]string{taglib.Title: {title}, taglib.Artist: {"Artist"}}
	}
	trackFile(t, dir, "synced.mp3", tags("Synced"))
	trackFile(t, dir, "plain.mp3", tags("Plain"))
	trackFile(t, dir, "missing.mp3", tags("Missing"))
	trackFile(t, dir, "broken.mp3", tags("Broken"))
	trackFile(t, dir, "untitled.mp3", map[string][]string{taglib.Artist: {"Artist"}})

	report, err := resolveLyrics(t.Context(), dir, logger.New(false), &fakeLyrics{
		byTitle: map[string]lyrics.Result{"Synced": {Synced: "[00:01.00]la"}, "Plain": {Plain: "la"}},
		errFor:  map[string]error{"Broken": errors.New("HTTP 503")},
	})
	if err != nil {
		t.Fatalf("resolveLyrics: %v", err)
	}

	got := [5]int{report.Synced, report.Plain, report.NotFound, report.Failed, report.Skipped}
	if got != [5]int{1, 1, 1, 1, 1} {
		t.Errorf("synced, plain, not found, failed, skipped = %v, want one of each", got)
	}
	if !strings.Contains(report.failureSummary(), "HTTP 503") {
		t.Errorf("summary %q does not say why", report.failureSummary())
	}
}

func TestResolveLyricsRefusesAFolderThatIsNotThere(t *testing.T) {
	if _, err := resolveLyrics(t.Context(), filepath.Join(t.TempDir(), "typo"), logger.New(false), &fakeLyrics{}); err == nil {
		t.Error("want an error, not a run that reports success on nothing")
	}
}
