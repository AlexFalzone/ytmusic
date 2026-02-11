package downloader

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"ytmusic/internal/config"
	"ytmusic/internal/logger"
)

func TestMergeFilesDeduplicate(t *testing.T) {
	tmpDir := t.TempDir()
	log := logger.New(false)
	d := New(config.DefaultConfig(), log, tmpDir)

	// Create two subdirectories with files that have the same name
	dir1 := filepath.Join(tmpDir, "artist1", "album1")
	dir2 := filepath.Join(tmpDir, "artist2", "album2")
	os.MkdirAll(dir1, 0755)
	os.MkdirAll(dir2, 0755)

	os.WriteFile(filepath.Join(dir1, "song.mp3"), []byte("content-1"), 0644)
	os.WriteFile(filepath.Join(dir2, "song.mp3"), []byte("content-2"), 0644)

	mergedDir, err := d.MergeFiles()
	if err != nil {
		t.Fatalf("MergeFiles() error: %v", err)
	}

	entries, err := os.ReadDir(mergedDir)
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) != 2 {
		t.Fatalf("expected 2 files in merged dir, got %d", len(entries))
	}

	// Verify both files exist with distinct names and original content
	names := make(map[string]bool)
	for _, e := range entries {
		names[e.Name()] = true
	}

	if !names["song.mp3"] {
		t.Error("expected song.mp3 in merged dir")
	}
	if !names["song_2.mp3"] {
		t.Error("expected song_2.mp3 in merged dir")
	}

	// Verify content is preserved (no data loss)
	b1, _ := os.ReadFile(filepath.Join(mergedDir, "song.mp3"))
	b2, _ := os.ReadFile(filepath.Join(mergedDir, "song_2.mp3"))
	contents := map[string]bool{string(b1): true, string(b2): true}
	if !contents["content-1"] || !contents["content-2"] {
		t.Error("file contents were lost during merge")
	}
}

func TestMergeFilesTripleDuplicate(t *testing.T) {
	tmpDir := t.TempDir()
	log := logger.New(false)
	d := New(config.DefaultConfig(), log, tmpDir)

	for i := 1; i <= 3; i++ {
		dir := filepath.Join(tmpDir, "artist", fmt.Sprintf("album%d", i))
		os.MkdirAll(dir, 0755)
		os.WriteFile(filepath.Join(dir, "track.mp3"), []byte(fmt.Sprintf("v%d", i)), 0644)
	}

	mergedDir, err := d.MergeFiles()
	if err != nil {
		t.Fatalf("MergeFiles() error: %v", err)
	}

	entries, err := os.ReadDir(mergedDir)
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) != 3 {
		t.Fatalf("expected 3 files, got %d", len(entries))
	}
}

func TestMergeFilesNoDuplicates(t *testing.T) {
	tmpDir := t.TempDir()
	log := logger.New(false)
	d := New(config.DefaultConfig(), log, tmpDir)

	dir := filepath.Join(tmpDir, "artist", "album")
	os.MkdirAll(dir, 0755)
	os.WriteFile(filepath.Join(dir, "song1.mp3"), []byte("a"), 0644)
	os.WriteFile(filepath.Join(dir, "song2.mp3"), []byte("b"), 0644)

	mergedDir, err := d.MergeFiles()
	if err != nil {
		t.Fatalf("MergeFiles() error: %v", err)
	}

	entries, err := os.ReadDir(mergedDir)
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) != 2 {
		t.Fatalf("expected 2 files, got %d", len(entries))
	}
}

func TestMergeFilesEmpty(t *testing.T) {
	tmpDir := t.TempDir()
	log := logger.New(false)
	d := New(config.DefaultConfig(), log, tmpDir)

	_, err := d.MergeFiles()
	if err == nil {
		t.Error("MergeFiles() should fail with no audio files")
	}
}
