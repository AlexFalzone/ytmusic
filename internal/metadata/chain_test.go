package metadata

import (
	"context"
	"fmt"
	"testing"

	"ytmusic/internal/logger"
)

type chainMockProvider struct {
	name    string
	results []TrackInfo
	err     error
}

func (m *chainMockProvider) Name() string { return m.name }
func (m *chainMockProvider) Search(_ context.Context, _ SearchQuery) ([]TrackInfo, error) {
	return m.results, m.err
}

func TestChainProvider_FirstSuccess(t *testing.T) {
	p1 := &chainMockProvider{name: "first", results: []TrackInfo{{Title: "from-first"}}}
	p2 := &chainMockProvider{name: "second", results: []TrackInfo{{Title: "from-second"}}}

	chain := NewChainProvider([]Provider{p1, p2}, logger.New(false))
	results, err := chain.Search(context.Background(), SearchQuery{Title: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 || results[0].Title != "from-first" {
		t.Errorf("expected result from first provider, got %v", results)
	}
}

func TestChainProvider_FallbackOnError(t *testing.T) {
	p1 := &chainMockProvider{name: "failing", err: fmt.Errorf("api down")}
	p2 := &chainMockProvider{name: "fallback", results: []TrackInfo{{Title: "from-fallback"}}}

	chain := NewChainProvider([]Provider{p1, p2}, logger.New(false))
	results, err := chain.Search(context.Background(), SearchQuery{Title: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 || results[0].Title != "from-fallback" {
		t.Errorf("expected result from fallback provider, got %v", results)
	}
}

func TestChainProvider_FallbackOnEmpty(t *testing.T) {
	p1 := &chainMockProvider{name: "empty", results: nil}
	p2 := &chainMockProvider{name: "has-results", results: []TrackInfo{{Title: "found"}}}

	chain := NewChainProvider([]Provider{p1, p2}, logger.New(false))
	results, err := chain.Search(context.Background(), SearchQuery{Title: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 || results[0].Title != "found" {
		t.Errorf("expected result from second provider, got %v", results)
	}
}

func TestChainProvider_AllFail(t *testing.T) {
	p1 := &chainMockProvider{name: "fail1", err: fmt.Errorf("error1")}
	p2 := &chainMockProvider{name: "fail2", err: fmt.Errorf("error2")}

	chain := NewChainProvider([]Provider{p1, p2}, logger.New(false))
	results, err := chain.Search(context.Background(), SearchQuery{Title: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if results != nil {
		t.Errorf("expected nil results, got %v", results)
	}
}

func TestChainProvider_Name(t *testing.T) {
	chain := NewChainProvider(nil, logger.New(false))
	if chain.Name() != "chain" {
		t.Errorf("Name() = %q, want %q", chain.Name(), "chain")
	}
}
