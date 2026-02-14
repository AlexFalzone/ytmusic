package metadata

import (
	"context"

	"ytmusic/internal/logger"
)

// ChainProvider tries multiple providers in order, returning results from
// the first one that succeeds with non-empty results.
type ChainProvider struct {
	providers []Provider
	logger    *logger.Logger
}

// NewChainProvider creates a ChainProvider that queries providers in order.
func NewChainProvider(providers []Provider, log *logger.Logger) *ChainProvider {
	return &ChainProvider{providers: providers, logger: log}
}

func (c *ChainProvider) Name() string { return "chain" }

func (c *ChainProvider) Search(ctx context.Context, query SearchQuery) ([]TrackInfo, error) {
	for _, p := range c.providers {
		results, err := p.Search(ctx, query)
		if err != nil {
			c.logger.Debug("provider %s failed: %v", p.Name(), err)
			continue
		}
		if len(results) > 0 {
			return results, nil
		}
	}
	return nil, nil
}
