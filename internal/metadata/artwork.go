package metadata

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"ytmusic/internal/buildinfo"
)

// A failed URL is excluded and gap filling reruns: MusicBrainz offers the same unchecked URL again as a filler.
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

func (r *Resolver) downloadArtwork(ctx context.Context, artworkURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, artworkURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create artwork request: %w", err)
	}
	req.Header.Set("User-Agent", buildinfo.UserAgent())

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to download artwork: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("artwork download returned %d", resp.StatusCode)
	}

	const maxArtworkSize = 10 << 20
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxArtworkSize))
	if err != nil {
		return nil, fmt.Errorf("failed to read artwork data: %w", err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("artwork download returned no data")
	}
	return data, nil
}
