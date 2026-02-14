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
	Config    config.Config
	Logger    *logger.Logger
	providers []metadata.Provider
}

// New creates a new Importer instance with the given metadata providers.
func New(cfg config.Config, log *logger.Logger, providers []metadata.Provider) *Importer {
	return &Importer{
		Config:    cfg,
		Logger:    log,
		providers: providers,
	}
}

// Import resolves metadata for all audio files in the given directory,
// then writes improved tags.
func (i *Importer) Import(ctx context.Context, dir string) error {
	i.Logger.Info("=== Resolving metadata ===")
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
	if err := resolver.Resolve(ctx, files); err != nil {
		return fmt.Errorf("metadata resolution failed: %w", err)
	}

	i.Logger.Info("Import completed")
	return nil
}
