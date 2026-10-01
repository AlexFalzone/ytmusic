package pipeline

import (
	"strings"
	"testing"

	"ytmusic/internal/config"
	"ytmusic/internal/logger"
	"ytmusic/internal/metadata"
)

// Checked as the importer receives it. A nil *Fingerprinter stored in an
// interface is not a nil interface: it passes the importer's nil check and the
// first file crashes on a nil receiver.
func TestNoFingerprinterWithoutAcoustID(t *testing.T) {
	c := buildComponents(config.Config{MetadataProviders: []string{"deezer"}}, logger.New(false))

	var fp metadata.Fingerprinter = c.fingerprinter
	if fp != nil {
		t.Fatalf("fingerprinter = %#v, want none without an AcoustID key", fp)
	}
}

func TestFingerprinterWithAcoustID(t *testing.T) {
	c := buildComponents(config.Config{AcoustIDAPIKey: "key"}, logger.New(false))

	if c.fingerprinter == nil {
		t.Fatal("fingerprinter missing with an AcoustID key")
	}
}

func TestCheckToolsNeedsFpcalcOnlyWithAcoustID(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	err := CheckTools(config.Config{})
	if err == nil {
		t.Fatal("want an error with nothing on PATH, got none")
	}
	for _, tool := range []string{"yt-dlp", "ffmpeg", "ffprobe"} {
		if !strings.Contains(err.Error(), tool) {
			t.Errorf("error should name %s, got: %v", tool, err)
		}
	}
	if strings.Contains(err.Error(), "fpcalc") {
		t.Errorf("fpcalc is required without an AcoustID key: %v", err)
	}

	err = CheckTools(config.Config{AcoustIDAPIKey: "key"})
	if err == nil || !strings.Contains(err.Error(), "fpcalc") {
		t.Errorf("want fpcalc required with an AcoustID key, got: %v", err)
	}
}

func TestCheckImportToolsNeedsNoDownloader(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	if err := CheckImportTools(config.Config{}); err != nil {
		t.Errorf("import without AcoustID needs no external tool, got: %v", err)
	}

	err := CheckImportTools(config.Config{AcoustIDAPIKey: "key"})
	if err == nil || !strings.Contains(err.Error(), "fpcalc") {
		t.Fatalf("want fpcalc required with an AcoustID key, got: %v", err)
	}
	if strings.Contains(err.Error(), "yt-dlp") {
		t.Errorf("import never downloads, yet yt-dlp is required: %v", err)
	}
}

// A dry run only lists the playlist through yt-dlp: it never converts audio or
// fingerprints, so it must not demand the tools for that.
func TestCheckToolsDryRunNeedsOnlyYtdlp(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	err := CheckTools(config.Config{DryRun: true, AcoustIDAPIKey: "key"})
	if err == nil || !strings.Contains(err.Error(), "yt-dlp") {
		t.Fatalf("want yt-dlp required, got: %v", err)
	}
	for _, tool := range []string{"ffmpeg", "ffprobe", "fpcalc"} {
		if strings.Contains(err.Error(), tool) {
			t.Errorf("dry run requires %s, which it never runs: %v", tool, err)
		}
	}
}
