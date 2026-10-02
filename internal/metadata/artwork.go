package metadata

import (
	"context"
	"fmt"
	"io"
	"net/http"
)

// complete fills the match's gaps and downloads its artwork. A URL that
// yields no image is set aside and gap filling runs again without it, so the
// next provider's artwork gets its chance: MusicBrainz hands out Cover Art
// Archive URLs unchecked, and offers a failed one again as a filler when its
// text search lands on the same release. Searches are remembered for the run,
// so running again sends no request.
func (r *Resolver) complete(ctx context.Context, src source, m match) (TrackInfo, []byte) {
	failed := make(map[string]bool)
	for {
		info := r.fillGaps(ctx, src, m, failed)
		if info.ArtworkURL == "" {
			if len(failed) > 0 {
				r.logger.Warn("  No artwork could be downloaded: %d URLs failed", len(failed))
			}
			return info, nil
		}

		art, err := r.downloadArtwork(ctx, info.ArtworkURL)
		if err == nil {
			return info, art
		}
		r.logger.Debug("  dropping artwork %s: %v", info.ArtworkURL, err)
		failed[info.ArtworkURL] = true
	}
}

// downloadArtwork fetches the image at artworkURL. An empty body counts as a
// failure: it is no more an image than a 404.
func (r *Resolver) downloadArtwork(ctx context.Context, artworkURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, artworkURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create artwork request: %w", err)
	}

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to download artwork: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("artwork download returned %d", resp.StatusCode)
	}

	const maxArtworkSize = 10 << 20 // 10 MB
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxArtworkSize))
	if err != nil {
		return nil, fmt.Errorf("failed to read artwork data: %w", err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("artwork download returned no data")
	}
	return data, nil
}
