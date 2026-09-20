package utils

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// writeFile creates a file with the given content, making parent directories.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestFindAudioFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.mp3"), "a")
	writeFile(t, filepath.Join(dir, "nested", "deep", "b.flac"), "b")
	writeFile(t, filepath.Join(dir, "c.MP3"), "c")
	writeFile(t, filepath.Join(dir, "cover.jpg"), "x")
	writeFile(t, filepath.Join(dir, "notes.txt"), "x")

	files, err := FindAudioFiles(dir)
	if err != nil {
		t.Fatalf("FindAudioFiles: %v", err)
	}

	var got []string
	for _, f := range files {
		rel, err := filepath.Rel(dir, f)
		if err != nil {
			t.Fatalf("Rel: %v", err)
		}
		got = append(got, rel)
	}
	sort.Strings(got)

	want := []string{"a.mp3", "c.MP3", filepath.Join("nested", "deep", "b.flac")}
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("found %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("found %v, want %v", got, want)
			break
		}
	}
}

func TestFindAudioFilesEmptyDir(t *testing.T) {
	files, err := FindAudioFiles(t.TempDir())
	if err != nil {
		t.Fatalf("FindAudioFiles: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("got %d files, want 0", len(files))
	}
}

func TestFindAudioFilesRejectsBadDir(t *testing.T) {
	if _, err := FindAudioFiles(""); err == nil {
		t.Error("empty path: expected error")
	}
	if _, err := FindAudioFiles(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("missing directory: expected error")
	}
}

func TestMoveFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mp3")
	writeFile(t, src, "payload")

	// The destination directory does not exist yet: MoveFile must create it.
	dst := filepath.Join(dir, "out", "Artist", "Album", "src.mp3")
	if err := MoveFile(src, dst); err != nil {
		t.Fatalf("MoveFile: %v", err)
	}

	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Error("source still exists after move")
	}
	content, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read destination: %v", err)
	}
	if string(content) != "payload" {
		t.Errorf("destination content = %q, want %q", content, "payload")
	}
}

func TestMoveFileRejectsBadInput(t *testing.T) {
	dir := t.TempDir()
	if err := MoveFile("", filepath.Join(dir, "x.mp3")); err == nil {
		t.Error("empty source: expected error")
	}
	if err := MoveFile(filepath.Join(dir, "x.mp3"), ""); err == nil {
		t.Error("empty destination: expected error")
	}
	if err := MoveFile(filepath.Join(dir, "missing.mp3"), filepath.Join(dir, "x.mp3")); err == nil {
		t.Error("missing source: expected error")
	}
}

// copyAndDelete is the cross-device fallback. Two filesystems cannot be
// arranged in a unit test, so it is called directly.
func TestCopyAndDelete(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mp3")
	writeFile(t, src, "payload")
	if err := os.Chmod(src, 0640); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	dst := filepath.Join(dir, "dst.mp3")
	if err := copyAndDelete(src, dst); err != nil {
		t.Fatalf("copyAndDelete: %v", err)
	}

	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Error("source still exists after copyAndDelete")
	}
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("stat destination: %v", err)
	}
	if info.Mode().Perm() != 0640 {
		t.Errorf("destination mode = %v, want %v", info.Mode().Perm(), os.FileMode(0640))
	}
	content, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read destination: %v", err)
	}
	if string(content) != "payload" {
		t.Errorf("destination content = %q, want %q", content, "payload")
	}
}

func TestMoveAudioFiles(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeFile(t, filepath.Join(src, "one.mp3"), "1")
	writeFile(t, filepath.Join(src, "two.flac"), "2")
	writeFile(t, filepath.Join(src, "cover.jpg"), "x")

	moved, failed, err := MoveAudioFiles(src, dst, nil)
	if err != nil {
		t.Fatalf("MoveAudioFiles: %v", err)
	}
	if moved != 2 || failed != 0 {
		t.Errorf("moved=%d failed=%d, want 2 and 0", moved, failed)
	}
	for _, name := range []string{"one.mp3", "two.flac"} {
		if _, err := os.Stat(filepath.Join(dst, name)); err != nil {
			t.Errorf("%s not in destination: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dst, "cover.jpg")); err == nil {
		t.Error("cover.jpg was moved, but it is not an audio file")
	}
}

func TestMoveAudioFilesWithSubDir(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeFile(t, filepath.Join(src, "song.mp3"), "1")

	moved, _, err := MoveAudioFiles(src, dst, func(string) string {
		return filepath.Join("Artist", "Album")
	})
	if err != nil {
		t.Fatalf("MoveAudioFiles: %v", err)
	}
	if moved != 1 {
		t.Errorf("moved = %d, want 1", moved)
	}
	if _, err := os.Stat(filepath.Join(dst, "Artist", "Album", "song.mp3")); err != nil {
		t.Errorf("file not in its subdirectory: %v", err)
	}
}

func TestMoveAudioFilesMovesLyricsSidecar(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeFile(t, filepath.Join(src, "song.mp3"), "audio")
	writeFile(t, filepath.Join(src, "song.lrc"), "lyrics")

	if _, _, err := MoveAudioFiles(src, dst, nil); err != nil {
		t.Fatalf("MoveAudioFiles: %v", err)
	}

	lrc, err := os.ReadFile(filepath.Join(dst, "song.lrc"))
	if err != nil {
		t.Fatalf("sidecar not moved next to the audio file: %v", err)
	}
	if string(lrc) != "lyrics" {
		t.Errorf("sidecar content = %q, want %q", lrc, "lyrics")
	}
}

func TestCleanupRemovesTempDir(t *testing.T) {
	dir, err := os.MkdirTemp("", "ytmusic-cleanup-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	writeFile(t, filepath.Join(dir, "a.mp3"), "a")

	if err := Cleanup(dir); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("temp directory still exists after Cleanup")
	}
}

func TestCleanupRefusesOutsideTemp(t *testing.T) {
	// A directory next to the package source, i.e. outside os.TempDir().
	dir, err := os.MkdirTemp(".", "ytmusic-outside-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatalf("Abs: %v", err)
	}
	defer os.RemoveAll(abs)

	if err := Cleanup(abs); err == nil {
		t.Error("expected an error for a directory outside the temp folder")
	}
	if _, err := os.Stat(abs); err != nil {
		t.Errorf("directory outside temp was deleted: %v", err)
	}
}

func TestCleanupEmptyPath(t *testing.T) {
	if err := Cleanup(""); err != nil {
		t.Errorf("Cleanup(\"\") = %v, want nil", err)
	}
}
