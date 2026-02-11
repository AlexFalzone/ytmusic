package metadata

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"go.senan.xyz/taglib"
)

// createTestAudioFile generates a minimal MP3 using ffmpeg.
// Skips the test if ffmpeg is not available.
func createTestAudioFile(t *testing.T, dir string) string {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available, skipping tagger test")
	}

	path := filepath.Join(dir, "test.mp3")
	cmd := exec.Command("ffmpeg", "-f", "lavfi", "-i", "anullsrc=r=44100:cl=mono", "-t", "0.1", "-q:a", "9", path)
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to create test audio file: %v", err)
	}
	return path
}

func TestWriteTags(t *testing.T) {
	dir := t.TempDir()
	path := createTestAudioFile(t, dir)

	info := TrackInfo{
		Title:       "Test Song",
		Artist:      "Test Artist",
		Album:       "Test Album",
		AlbumArtist: "Test Album Artist",
		TrackNumber: 3,
		DiscNumber:  1,
		Year:        2023,
		Genre:       "Pop",
	}

	if err := WriteTags(path, info); err != nil {
		t.Fatalf("WriteTags failed: %v", err)
	}

	// Verify written tags
	tags, err := taglib.ReadTags(path)
	if err != nil {
		t.Fatalf("failed to read tags: %v", err)
	}

	checks := map[string]string{
		taglib.Title:       "Test Song",
		taglib.Artist:      "Test Artist",
		taglib.Album:       "Test Album",
		taglib.AlbumArtist: "Test Album Artist",
		taglib.TrackNumber: "3",
		taglib.DiscNumber:  "1",
		taglib.Date:        "2023",
		taglib.Genre:       "Pop",
	}

	for key, want := range checks {
		got := ""
		if vals, ok := tags[key]; ok && len(vals) > 0 {
			got = vals[0]
		}
		if got != want {
			t.Errorf("tag %s = %q, want %q", key, got, want)
		}
	}
}

func TestWriteArtwork(t *testing.T) {
	dir := t.TempDir()
	path := createTestAudioFile(t, dir)

	// Minimal valid JPEG (smallest valid JFIF)
	fakeImage := []byte{
		0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 0x4A, 0x46, 0x49, 0x46, 0x00, 0x01,
		0x01, 0x00, 0x00, 0x01, 0x00, 0x01, 0x00, 0x00, 0xFF, 0xD9,
	}

	if err := WriteArtwork(path, fakeImage); err != nil {
		t.Fatalf("WriteArtwork failed: %v", err)
	}

	data, err := taglib.ReadImage(path)
	if err != nil {
		t.Fatalf("failed to read image: %v", err)
	}
	if len(data) == 0 {
		t.Error("expected embedded image data, got empty")
	}
}

func TestWriteArtworkEmpty(t *testing.T) {
	// Should be a no-op with empty data
	if err := WriteArtwork("/nonexistent", nil); err != nil {
		t.Errorf("expected nil error for empty image, got %v", err)
	}
}

func TestWriteTagsNonexistentFile(t *testing.T) {
	err := WriteTags("/nonexistent/file.mp3", TrackInfo{Title: "x"})
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestWriteTagsEmptyInfo(t *testing.T) {
	dir := t.TempDir()
	path := createTestAudioFile(t, dir)

	// Writing empty info should not error (just writes nothing)
	if err := WriteTags(path, TrackInfo{}); err != nil {
		t.Fatalf("WriteTags with empty info failed: %v", err)
	}

	// Verify file still readable
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file missing after empty write: %v", err)
	}
}
