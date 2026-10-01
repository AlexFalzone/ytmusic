package metadata

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"ytmusic/internal/logger"

	"go.senan.xyz/taglib"
)

// panickingProvider panics on one title and matches every other.
type panickingProvider struct{ on string }

func (p panickingProvider) Name() string { return "panicking" }

func (p panickingProvider) Search(_ context.Context, q SearchQuery) ([]TrackInfo, error) {
	if q.Title == p.on {
		panic("provider bug")
	}
	return []TrackInfo{{Title: q.Title, Artist: q.Artist, Album: "Album"}}, nil
}

// The per-file workers run off any handler stack: a panic there would take
// the web server down with every job on it.
func TestResolve_PanicFailsOnlyItsFile(t *testing.T) {
	boom, fine := newTestMP3(t), newTestMP3(t)
	tagTestFile(t, boom, "Boom", "Artist")
	tagTestFile(t, fine, "Fine", "Artist")

	r := NewResolver([]Provider{panickingProvider{on: "Boom"}}, logger.New(false), 0).WithWorkers(2)
	if err := r.Resolve(context.Background(), []string{boom, fine}); err != nil {
		t.Fatalf("Resolve = %v, want nil: one file of two still resolved", err)
	}

	if got := readTestTag(t, fine, taglib.Album); got != "Album" {
		t.Errorf("Album of the other file = %q, want %q", got, "Album")
	}
}

// cancellingProvider cancels the run on its first search, as a user stopping
// the job mid-way would.
type cancellingProvider struct {
	cancel context.CancelFunc
	calls  atomic.Int32
}

func (p *cancellingProvider) Name() string { return "cancelling" }

func (p *cancellingProvider) Search(_ context.Context, q SearchQuery) ([]TrackInfo, error) {
	p.calls.Add(1)
	p.cancel()
	return []TrackInfo{{Title: q.Title, Artist: q.Artist, Album: "Album"}}, nil
}

func TestResolve_StartsNoFileOnceCancelled(t *testing.T) {
	paths := []string{newTestMP3(t), newTestMP3(t), newTestMP3(t)}
	for _, p := range paths {
		tagTestFile(t, p, "Song", "Artist")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := &cancellingProvider{cancel: cancel}

	err := NewResolver([]Provider{p}, logger.New(false), 0).WithWorkers(1).Resolve(ctx, paths)

	if !errors.Is(err, context.Canceled) {
		t.Errorf("Resolve = %v, want context.Canceled", err)
	}
	if n := p.calls.Load(); n != 1 {
		t.Errorf("provider searched %d times, want 1: no file may start after the cancel", n)
	}
}
