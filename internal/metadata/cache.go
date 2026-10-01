package metadata

import (
	"context"
	"slices"

	"ytmusic/internal/memo"
)

// cachedProvider remembers a provider's answers for the run. A file that
// declares a variant asks every provider for its primary match and then the
// later ones again to fill its gaps; a playlist can also hold a song twice.
type cachedProvider struct {
	Provider
	results memo.Cache[SearchQuery, []TrackInfo]
}

// Search returns a copy of the remembered results, so that files resolved in
// parallel never share a slice one of them might edit.
func (p *cachedProvider) Search(ctx context.Context, q SearchQuery) ([]TrackInfo, error) {
	results, err := p.results.Do(q, func() ([]TrackInfo, error) {
		return p.Provider.Search(ctx, q)
	})
	return slices.Clone(results), err
}
