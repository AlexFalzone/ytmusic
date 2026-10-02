package metadata

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"go.senan.xyz/taglib"
)

func TestWriteTagMap(t *testing.T) {
	path := newTestMP3(t)

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

	var s tagStore
	if err := s.write(path, tagMap(info)); err != nil {
		t.Fatalf("write: %v", err)
	}

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
	path := newTestMP3(t)

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
	if err := WriteArtwork("/nonexistent", nil); err != nil {
		t.Errorf("expected nil error for empty image, got %v", err)
	}
}

func TestWriteTagMapNonexistentFile(t *testing.T) {
	var s tagStore
	err := s.write("/nonexistent/file.mp3", tagMap(TrackInfo{Title: "x"}))
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestWriteTagMapEmptyInfo(t *testing.T) {
	path := newTestMP3(t)

	var s tagStore
	if err := s.write(path, tagMap(TrackInfo{})); err != nil {
		t.Fatalf("write with empty info failed: %v", err)
	}

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
		{"reserved with extension", "CON.mp3", "CON.mp3_"},
		{"not reserved with extension", "CONCERT.mp3", "CONCERT.mp3"},
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
	// Two-byte runes: 255 is odd, so a naive s[:255] splits a rune (three-byte ones would land on a boundary).
	long := strings.Repeat("é", 200)
	got := sanitizePath(long)

	if len(got) > maxComponentBytes {
		t.Errorf("result is %d bytes, want at most %d", len(got), maxComponentBytes)
	}
	if !utf8.ValidString(got) {
		t.Errorf("truncation split a rune: %q is not valid UTF-8", got)
	}
	if r, _ := utf8.DecodeLastRuneInString(got); r == utf8.RuneError {
		t.Errorf("the last rune of %q is broken", got)
	}
	if want := 127; utf8.RuneCountInString(got) != want {
		t.Errorf("kept %d runes, want %d", utf8.RuneCountInString(got), want)
	}
}

func TestSubDirFromTagsCannotEscapeOutputDir(t *testing.T) {
	path := newTestMP3(t)

	writeTestTags(t, path, tagMap(TrackInfo{Artist: "..", Album: "..", AlbumArtist: ".."}))

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

func TestMergeWithExisting_HandlesSlashTrackNumberFormat(t *testing.T) {
	tags := map[string][]string{
		taglib.Title:       {"TRUST!"},
		taglib.TrackNumber: {"5/12"},
	}

	info := TrackInfo{Title: "TRUST!", TrackNumber: 3}
	got := mergeWithExisting(tags, info)

	if got.TrackNumber != 5 {
		t.Errorf("TrackNumber = %d, want 5 (slash format must be parsed, not ignored)", got.TrackNumber)
	}
}

func TestMergeWithExisting_PreservesNonZeroTrackNumber(t *testing.T) {
	tags := map[string][]string{
		taglib.Title:       {"TRUST!"},
		taglib.TrackNumber: {"5"},
	}

	info := TrackInfo{Title: "TRUST!", TrackNumber: 3}
	got := mergeWithExisting(tags, info)

	if got.TrackNumber != 5 {
		t.Errorf("TrackNumber = %d, want 5 (yt-dlp value preserved)", got.TrackNumber)
	}
}

func TestMergeWithExisting_FillsZeroTrackNumber(t *testing.T) {
	tags := map[string][]string{
		taglib.Title: {"TRUST!"},
	}

	info := TrackInfo{Title: "TRUST!", TrackNumber: 5}
	got := mergeWithExisting(tags, info)

	if got.TrackNumber != 5 {
		t.Errorf("TrackNumber = %d, want 5 (provider value used when no existing tag)", got.TrackNumber)
	}
}

func TestMergeWithExisting_PreservesNonZeroDiscNumber(t *testing.T) {
	tags := map[string][]string{
		taglib.Title:      {"TRUST!"},
		taglib.DiscNumber: {"1"},
	}

	info := TrackInfo{Title: "TRUST!", DiscNumber: 2}
	got := mergeWithExisting(tags, info)

	if got.DiscNumber != 1 {
		t.Errorf("DiscNumber = %d, want 1 (yt-dlp value preserved)", got.DiscNumber)
	}
}
