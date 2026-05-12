package importer

import (
	"context"
	"fmt"
	"os"

	"ytmusic/internal/config"
	"ytmusic/internal/logger"
	"ytmusic/internal/metadata"
	"ytmusic/pkg/utils"
)

// Importer handles resolving and writing metadata for downloaded audio files.
type Importer struct {
	Config             config.Config
	Logger             *logger.Logger
	providers          []metadata.Provider
	fingerprinter      metadata.Fingerprinter      // nil if not configured
	albumResolver      metadata.AlbumResolver      // nil if not configured
	batchFingerprinter metadata.BatchFingerprinter // nil if not configured
	releaseResolver    metadata.ReleaseResolver    // nil if not configured
}

// New creates a new Importer instance with the given metadata providers.
func New(cfg config.Config, log *logger.Logger, providers []metadata.Provider, fp metadata.Fingerprinter) *Importer {
	return &Importer{
		Config:        cfg,
		Logger:        log,
		providers:     providers,
		fingerprinter: fp,
	}
}

// WithAlbumResolver attaches an album resolver for the album-first positional-tag phase.
func (i *Importer) WithAlbumResolver(ar metadata.AlbumResolver) *Importer {
	i.albumResolver = ar
	return i
}

// WithBatchFingerprinter attaches a batch fingerprinter for the batch-fingerprint phase.
func (i *Importer) WithBatchFingerprinter(bf metadata.BatchFingerprinter) *Importer {
	i.batchFingerprinter = bf
	return i
}

// WithReleaseResolver attaches a release resolver for dominant-release detection.
func (i *Importer) WithReleaseResolver(rr metadata.ReleaseResolver) *Importer {
	i.releaseResolver = rr
	return i
}

// Import resolves metadata for all audio files in the given directory,
// then writes improved tags.
func (i *Importer) Import(ctx context.Context, dir string) error {
	i.Logger.Info("resolving metadata")
	i.Logger.Debug("Folder: %s", dir)

	if dir == "" {
		return fmt.Errorf("import directory cannot be empty")
	}
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("import directory does not exist: %s", dir)
	}

	files, err := utils.FindAudioFiles(dir)
	if err != nil {
		return fmt.Errorf("failed to find audio files: %w", err)
	}
	if len(files) == 0 {
		return fmt.Errorf("no audio files found in %s", dir)
	}

	i.Logger.Debug("Found %d audio files", len(files))

	resolver := metadata.NewResolver(i.providers, i.Logger, i.Config.ConfidenceThreshold)
	if i.fingerprinter != nil {
		resolver = resolver.WithFingerprinter(i.fingerprinter)
	}
	if i.albumResolver != nil {
		resolver = resolver.WithAlbumResolver(i.albumResolver)
	}
	if i.batchFingerprinter != nil {
		resolver = resolver.WithBatchFingerprinter(i.batchFingerprinter)
	}
	if i.releaseResolver != nil {
		resolver = resolver.WithReleaseResolver(i.releaseResolver)
	}
	if err := resolver.Resolve(ctx, files); err != nil {
		return fmt.Errorf("metadata resolution failed: %w", err)
	}

	i.Logger.Info("Import completed")
	return nil
}
