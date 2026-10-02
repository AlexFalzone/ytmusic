package pipeline

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"ytmusic/internal/logger"
	"ytmusic/internal/lyrics"
	"ytmusic/internal/metadata"
	"ytmusic/internal/testaudio"

	"go.senan.xyz/taglib"
)

// fakeLyrics answers by title and records what it was asked.
type fakeLyrics struct {
	byTitle map[string]lyrics.Result
	mu      sync.Mutex
	asked   []string
}

func (f *fakeLyrics) Fetch(_ context.Context, _, title, _ string) (lyrics.Result, error) {
	f.mu.Lock()
	f.asked = append(f.asked, title)
	f.mu.Unlock()
	return f.byTitle[title], nil
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
	resolveLyrics(context.Background(), dir, logger.New(false), f)

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
	if got, _ := os.ReadFile(doneLRC); string(got) != "old" {
		t.Errorf("done.lrc = %q, want it left alone", got)
	}
	if slices.Contains(f.asked, "Done") || slices.Contains(f.asked, "Lonely") {
		t.Errorf("asked for %v: a track with lyrics already, or without an artist, needs no lookup", f.asked)
	}
}
