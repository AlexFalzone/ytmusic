// Package testaudio makes the audio files tests run on. Only tests import it.
package testaudio

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// MP3 writes a silent MP3 lasting seconds ("0.1") to dir/name and returns its
// path. The test is skipped when ffmpeg is not installed.
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
