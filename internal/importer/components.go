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

type fingerprinter interface {
	metadata.Fingerprinter
	metadata.BatchFingerprinter
}

type components struct {
	providers       []metadata.Provider
	fingerprinter   fingerprinter
	albumResolver   metadata.AlbumResolver
	releaseResolver metadata.ReleaseResolver
}

// One MusicBrainz client for everything: its throttle and caches live in the instance.
func buildComponents(cfg config.Config) components {
	var mbClient *musicbrainz.Client
	if slices.Contains(cfg.MetadataProviders, "musicbrainz") || cfg.AcoustIDAPIKey != "" {
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

	// The interface, not *fingerprint.Fingerprinter: a nil pointer in an interface is not nil.
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
