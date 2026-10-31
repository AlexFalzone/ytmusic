package fingerprint_test

import (
	"os/exec"
	"strings"
	"testing"

	"ytmusic/internal/fingerprint"
	"ytmusic/internal/testaudio"
)

// The real binary, not a fake: only it says which output format fpcalc prints.
func TestGenerate_ReadsWhatFpcalcPrints(t *testing.T) {
	if _, err := exec.LookPath("fpcalc"); err != nil {
		t.Skip("fpcalc not installed")
	}
	path := testaudio.MP3(t, t.TempDir(), "silence.mp3", "30")

	fp, err := fingerprint.Generate(t.Context(), path)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !strings.HasPrefix(fp.Fingerprint, "AQ") {
		t.Errorf("Fingerprint = %.20q…, want the compressed form starting with AQ", fp.Fingerprint)
	}
	if fp.Duration != 30 {
		t.Errorf("Duration = %d, want 30", fp.Duration)
	}
}

func TestGenerate_MissingFile(t *testing.T) {
	if _, err := exec.LookPath("fpcalc"); err != nil {
		t.Skip("fpcalc not installed")
	}
	_, err := fingerprint.Generate(t.Context(), "/nonexistent/file.mp3")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestGenerate_InvalidFile(t *testing.T) {
	if _, err := exec.LookPath("fpcalc"); err != nil {
		t.Skip("fpcalc not installed")
	}
	_, err := fingerprint.Generate(t.Context(), "/etc/hostname")
	if err == nil {
		t.Fatal("expected error for non-audio file, got nil")
	}
}
