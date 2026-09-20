package metadata

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

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

func TestSanitizePathHostileInput(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"parent directory", "..", "_"},
		{"current directory", ".", "_"},
		{"deeper traversal", "../..", "_"},
		{"newline", "Song\nTitle", "SongTitle"},
		{"tab", "Song\tTitle", "SongTitle"},
		{"null byte", "Song\x00Title", "SongTitle"},
		{"leading dot", ".hidden", "hidden"},
		{"trailing dot", "Album.", "Album"},
		{"surrounding spaces", "  Album  ", "Album"},
		{"only dots and spaces", " . . ", "_"},
		{"separators still replaced", "AC/DC", "AC_DC"},
		{"windows reserved name", "CON", "CON_"},
		{"windows reserved lowercase", "nul", "nul_"},
		{"windows reserved with digit", "COM4", "COM4_"},
		{"not reserved", "CONCERT", "CONCERT"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sanitizePath(tt.input); got != tt.want {
				t.Errorf("sanitizePath(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestSanitizePathTruncatesOnRuneBoundary(t *testing.T) {
	// 200 three-byte runes: 600 bytes, well past the 255-byte limit.
	long := strings.Repeat("あ", 200)
	got := sanitizePath(long)

	if len(got) > 255 {
		t.Errorf("result is %d bytes, want at most 255", len(got))
	}
	if !utf8.ValidString(got) {
		t.Errorf("truncation split a rune: %q is not valid UTF-8", got)
	}
	if got == "" {
		t.Error("truncation emptied the name")
	}
}

func TestSubDirFromTagsCannotEscapeOutputDir(t *testing.T) {
	dir := t.TempDir()
	path := createTestAudioFile(t, dir)

	if err := WriteTags(path, TrackInfo{Artist: "..", Album: "..", AlbumArtist: ".."}); err != nil {
		t.Fatalf("WriteTags: %v", err)
	}

	sub := SubDirFromTags(path)
	joined := filepath.Join("/music", sub)
	rel, err := filepath.Rel("/music", joined)
	if err != nil {
		t.Fatalf("Rel: %v", err)
	}
	if rel == ".." || strings.HasPrefix(rel, "..") {
		t.Errorf("SubDirFromTags returned %q, which escapes the output directory (rel = %q)", sub, rel)
	}
}
