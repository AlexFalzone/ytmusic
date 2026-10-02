package pipeline

import (
	"ytmusic/internal/config"
	"ytmusic/pkg/utils"
)

// A missing fpcalc would otherwise show up only as worse tags, never as an error.
func CheckTools(cfg config.Config) error {
	if cfg.DryRun {
		return utils.CheckDependencies("yt-dlp")
	}
	return utils.CheckDependencies(append([]string{"yt-dlp", "ffmpeg", "ffprobe"}, fingerprintTools(cfg)...)...)
}

func CheckImportTools(cfg config.Config) error {
	return utils.CheckDependencies(fingerprintTools(cfg)...)
}

func fingerprintTools(cfg config.Config) []string {
	if cfg.AcoustIDAPIKey == "" {
		return nil
	}
	return []string{"fpcalc"}
}
