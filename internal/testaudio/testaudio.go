package testaudio

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// Skips the test without ffmpeg.
func MP3(t testing.TB, dir, name, seconds string) string {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available")
	}
	path := filepath.Join(dir, name)
	cmd := exec.Command("ffmpeg", "-f", "lavfi", "-i", "anullsrc=r=44100:cl=mono", "-t", seconds, "-q:a", "9", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v\n%s", err, out)
	}
	return path
}
