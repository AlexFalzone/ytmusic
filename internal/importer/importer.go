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

type Importer struct {
	log      *logger.Logger
	resolver *metadata.Resolver // nil: nothing to resolve with
}

func New(cfg config.Config, log *logger.Logger) *Importer {
	return newImporter(cfg, log, buildComponents(cfg))
}

func newImporter(cfg config.Config, log *logger.Logger, c components) *Importer {
	imp := &Importer{log: log}
	if len(c.providers) == 0 && c.fingerprinter == nil {
		return imp
	}

	r := metadata.NewResolver(c.providers, log, cfg.ConfidenceThreshold).WithWorkers(cfg.MetadataWorkers)
	if c.fingerprinter != nil {
		r.WithFingerprinter(c.fingerprinter).WithBatchFingerprinter(c.fingerprinter)
	}
	if c.albumResolver != nil {
		r.WithAlbumResolver(c.albumResolver)
	}
	if c.releaseResolver != nil {
		r.WithReleaseResolver(c.releaseResolver)
	}
	imp.resolver = r
	return imp
}

func (i *Importer) Import(ctx context.Context, dir string) error {
	if i.resolver == nil {
		i.log.Info("No metadata providers configured, skipping metadata resolution")
		return nil
	}

	i.log.Info("resolving metadata")
	i.log.Debug("Folder: %s", dir)

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
	i.log.Debug("Found %d audio files", len(files))

	if err := i.resolver.Resolve(ctx, files); err != nil {
		return fmt.Errorf("resolving %s: %w", dir, err)
	}

	i.log.Info("Import completed")
	return nil
}
