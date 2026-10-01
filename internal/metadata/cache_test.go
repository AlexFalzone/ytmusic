package metadata

import (
	"context"
	"sync/atomic"
	"testing"

	"ytmusic/internal/logger"
)

type countingProvider struct {
	name    string
	results []TrackInfo
	calls   atomic.Int32
}

func (p *countingProvider) Name() string { return p.name }

func (p *countingProvider) Search(_ context.Context, _ SearchQuery) ([]TrackInfo, error) {
	p.calls.Add(1)
	return p.results, nil
}

// A variant file asks every provider for its primary match, then asks the
// ones after the donor again to fill its gaps: the same query, twice.
func TestResolverAsksEachProviderOncePerQuery(t *testing.T) {
	p1 := &countingProvider{name: "first", results: []TrackInfo{{Title: "Song", Artist: "Artist", Album: "Album"}}}
	p2 := &countingProvider{name: "second", results: []TrackInfo{{Title: "Song", Artist: "Artist", Album: "Album"}}}
	r := NewResolver([]Provider{p1, p2}, logger.New(false), 0.5)

	m, ok := r.findPrimaryMatch(context.Background(), liveSource())
	if !ok || !m.donor {
		t.Fatalf("match = %+v, ok = %v, want a donor", m, ok)
	}
	r.fillGaps(context.Background(), liveSource(), m)

	if n := p2.calls.Load(); n != 1 {
		t.Errorf("second provider asked %d times, want 1", n)
	}
}

// Workers share the cached results: one of them editing its copy must not
// change what the next one is given.
func TestCachedResultsAreCopies(t *testing.T) {
	p := &cachedProvider{Provider: &countingProvider{name: "p", results: []TrackInfo{{Title: "Song"}}}}
	q := SearchQuery{Title: "Song"}

	first, err := p.Search(context.Background(), q)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	first[0].Title = "Edited"

	second, err := p.Search(context.Background(), q)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if second[0].Title != "Song" {
		t.Errorf("Title = %q, want %q", second[0].Title, "Song")
	}
}
