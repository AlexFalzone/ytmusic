package fingerprint_test

import (
	"context"
	"os/exec"
	"testing"

	"ytmusic/internal/fingerprint"
)

func TestGenerate_MissingFile(t *testing.T) {
	if _, err := exec.LookPath("fpcalc"); err != nil {
		t.Skip("fpcalc not installed")
	}
	_, err := fingerprint.Generate(context.Background(), "/nonexistent/file.mp3")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestGenerate_InvalidFile(t *testing.T) {
	if _, err := exec.LookPath("fpcalc"); err != nil {
		t.Skip("fpcalc not installed")
	}
	// /etc/hostname is not an audio file
	_, err := fingerprint.Generate(context.Background(), "/etc/hostname")
	if err == nil {
		t.Fatal("expected error for non-audio file, got nil")
	}
}
