package importer

import (
	"context"
	"slices"

	"ytmusic/internal/config"
	"ytmusic/internal/fingerprint"
	"ytmusic/internal/metadata"
	"ytmusic/internal/provider/deezer"
	"ytmusic/internal/provider/itunes"
	"ytmusic/internal/provider/musicbrainz"
	"ytmusic/internal/provider/spotify"
)

// fingerprinter is what AcoustID identification provides: single-file and
// batch lookups.
type fingerprinter interface {
	metadata.Fingerprinter
	metadata.BatchFingerprinter
}

type components struct {
	providers       []metadata.Provider
	fingerprinter   fingerprinter            // nil if AcoustID not configured
	albumResolver   metadata.AlbumResolver   // nil if musicbrainz not in providers
	releaseResolver metadata.ReleaseResolver // nil if musicbrainz not in providers
}

// buildComponents creates all metadata-related components, sharing a single
// MusicBrainz client so its rate limiter is coordinated across all usages.
func buildComponents(cfg config.Config) components {
	var mbClient *musicbrainz.Client
	if slices.Contains(cfg.MetadataProviders, "musicbrainz") {
		mbClient = musicbrainz.New()
	}
	// Also need a MusicBrainz client for fingerprint MBID lookups even when the
	// musicbrainz search provider is not in the provider list.
	if mbClient == nil && cfg.AcoustIDAPIKey != "" {
		mbClient = musicbrainz.New()
	}

	var providers []metadata.Provider
	for _, name := range cfg.MetadataProviders {
		switch name {
		case "spotify":
			providers = append(providers, spotify.New(cfg.SpotifyClientID, cfg.SpotifyClientSecret))
		case "musicbrainz":
			providers = append(providers, mbClient)
		case "deezer":
			providers = append(providers, deezer.New())
		case "itunes":
			providers = append(providers, itunes.New())
		}
	}

	// Declared as the interface, not *fingerprint.Fingerprinter: a nil pointer
	// converted to an interface is not nil and would crash the resolver.
	var fp fingerprinter
	if cfg.AcoustIDAPIKey != "" {
		acoustid := fingerprint.NewAcoustIDClient(cfg.AcoustIDAPIKey, "")
		fp = fingerprint.New(acoustid, func(ctx context.Context, mbid, preferAlbum string) (metadata.TrackInfo, error) {
			return mbClient.LookupByMBID(ctx, mbid, preferAlbum)
		})
	}

	var ar metadata.AlbumResolver
	var rr metadata.ReleaseResolver
	if mbClient != nil {
		ar = mbClient
		rr = mbClient
	}

	return components{
		providers:       providers,
		fingerprinter:   fp,
		albumResolver:   ar,
		releaseResolver: rr,
	}
}
