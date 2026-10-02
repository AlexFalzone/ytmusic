package metadata

import (
	"context"
	"slices"

	"ytmusic/internal/memo"
)

type cachedProvider struct {
	Provider
	results memo.Cache[SearchQuery, []TrackInfo]
}

// A copy: files resolved in parallel must not share a slice one of them might edit.
func (p *cachedProvider) Search(ctx context.Context, q SearchQuery) ([]TrackInfo, error) {
	results, err := p.results.Do(q, func() ([]TrackInfo, error) {
		return p.Provider.Search(ctx, q)
	})
	return slices.Clone(results), err
}
