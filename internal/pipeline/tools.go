package pipeline

import (
	"ytmusic/internal/config"
	"ytmusic/pkg/utils"
)

// CheckTools verifies that every external program Run needs is on PATH:
// yt-dlp, the FFmpeg pair it extracts audio with, and fpcalc when AcoustID is
// configured. Without that last check a missing fpcalc only shows up as worse
// tags, never as an error. A dry run only lists the playlist, so yt-dlp is all
// it needs.
func CheckTools(cfg config.Config) error {
	if cfg.DryRun {
		return utils.CheckDependencies("yt-dlp")
	}
	return utils.CheckDependencies(append([]string{"yt-dlp", "ffmpeg", "ffprobe"}, fingerprintTools(cfg)...)...)
}

// CheckImportTools verifies the external programs RunImportOnly needs. It never
// downloads, so only the fingerprinter can be missing.
func CheckImportTools(cfg config.Config) error {
	return utils.CheckDependencies(fingerprintTools(cfg)...)
}

func fingerprintTools(cfg config.Config) []string {
	if cfg.AcoustIDAPIKey == "" {
		return nil
	}
	return []string{"fpcalc"}
}
