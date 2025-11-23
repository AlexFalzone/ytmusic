package importer

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"ytmusic/internal/config"
	"ytmusic/internal/logger"
)

// Importer handles importing music files into the beets library
type Importer struct {
	Config config.Config
	Logger *logger.Logger
}

// New creates a new Importer instance
func New(cfg config.Config, log *logger.Logger) *Importer {
	return &Importer{
		Config: cfg,
		Logger: log,
	}
}

// Import runs beets import on the specified folder.
// Automatically responds to prompts:
// - "A" (Apply) for album matches
// - "R" (Remove old) for duplicates (safer than Merge, handles missing files)
func (i *Importer) Import(ctx context.Context, dir string) error {
	i.Logger.Info("=== Importing with beets ===")
	i.Logger.Debug("Folder: %s", dir)

	if dir == "" {
		return fmt.Errorf("import directory cannot be empty")
	}
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("import directory does not exist: %s", dir)
	}

	args := []string{"-m", "beets", "import", "--move", dir}
	cmd := exec.CommandContext(ctx, "python3", args...)

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Automated responses prioritizing duplicates over no-match
	// A = Apply for matches
	// R = Remove old for duplicates (most common)
	// S = Skip for no matches (rare)
	var autoResponses string
	for i := 0; i < 100; i++ {
		autoResponses += "A\n"
		autoResponses += "R\n"
		autoResponses += "R\n"
		autoResponses += "R\n"
		autoResponses += "S\n"
	}
	cmd.Stdin = strings.NewReader(autoResponses)

	err := cmd.Run()
	if ctx.Err() != nil {
		return fmt.Errorf("import cancelled")
	}
	if err != nil {
		return fmt.Errorf("beets import failed: %w", err)
	}

	i.Logger.Info("Import completed")
	return nil
}
