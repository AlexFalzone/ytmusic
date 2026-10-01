package utils

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
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

	moved, failed, _, err := MoveAudioFiles(src, dst, nil)
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

	moved, _, _, err := MoveAudioFiles(src, dst, func(string) string {
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

	if _, _, _, err := MoveAudioFiles(src, dst, nil); err != nil {
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
	// A directory that is not under the temp folder, arranged by pointing the
	// temp folder elsewhere rather than by writing into the source tree.
	base := t.TempDir()
	tmp := filepath.Join(base, "tmp")
	abs := filepath.Join(base, "elsewhere")
	writeFile(t, filepath.Join(tmp, "keep"), "x")
	writeFile(t, filepath.Join(abs, "keep"), "x")
	t.Setenv("TMPDIR", tmp)

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

func TestMoveAudioFilesKeepsTheExistingFile(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeFile(t, filepath.Join(dst, "song.mp3"), "already here")
	writeFile(t, filepath.Join(src, "song.mp3"), "the new one")

	moved, failed, _, err := MoveAudioFiles(src, dst, nil)
	if err != nil {
		t.Fatalf("MoveAudioFiles: %v", err)
	}
	if moved != 1 || failed != 0 {
		t.Errorf("moved=%d failed=%d, want 1 and 0", moved, failed)
	}

	old, err := os.ReadFile(filepath.Join(dst, "song.mp3"))
	if err != nil {
		t.Fatalf("read existing file: %v", err)
	}
	if string(old) != "already here" {
		t.Errorf("existing file was overwritten: content = %q", old)
	}

	added, err := os.ReadFile(filepath.Join(dst, "song_2.mp3"))
	if err != nil {
		t.Fatalf("new file not stored under a free name: %v", err)
	}
	if string(added) != "the new one" {
		t.Errorf("new file content = %q, want %q", added, "the new one")
	}
}

func TestMoveAudioFilesNumbersEveryCollision(t *testing.T) {
	dst := t.TempDir()
	for i, content := range []string{"first", "second", "third"} {
		src := t.TempDir()
		writeFile(t, filepath.Join(src, "song.mp3"), content)
		if _, _, _, err := MoveAudioFiles(src, dst, nil); err != nil {
			t.Fatalf("move %d: %v", i, err)
		}
	}

	want := map[string]string{
		"song.mp3":   "first",
		"song_2.mp3": "second",
		"song_3.mp3": "third",
	}
	for name, content := range want {
		got, err := os.ReadFile(filepath.Join(dst, name))
		if err != nil {
			t.Errorf("%s missing: %v", name, err)
			continue
		}
		if string(got) != content {
			t.Errorf("%s = %q, want %q", name, got, content)
		}
	}
}

func TestMoveAudioFilesSidecarFollowsTheResolvedName(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeFile(t, filepath.Join(dst, "song.mp3"), "already here")
	writeFile(t, filepath.Join(src, "song.mp3"), "the new one")
	writeFile(t, filepath.Join(src, "song.lrc"), "the new lyrics")

	if _, _, _, err := MoveAudioFiles(src, dst, nil); err != nil {
		t.Fatalf("MoveAudioFiles: %v", err)
	}

	// An orphan sidecar keeping the old name is what no player would ever pair
	// with the track it belongs to.
	if _, err := os.Stat(filepath.Join(dst, "song.lrc")); err == nil {
		t.Error("sidecar kept the original name, leaving it orphaned")
	}
	lrc, err := os.ReadFile(filepath.Join(dst, "song_2.lrc"))
	if err != nil {
		t.Fatalf("sidecar did not follow the audio file: %v", err)
	}
	if string(lrc) != "the new lyrics" {
		t.Errorf("sidecar content = %q, want %q", lrc, "the new lyrics")
	}
}

func TestUniquePathOddNames(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name string
		base string
		want string
	}{
		{"no extension", "song", "song_2"},
		{"double extension", "song.tar.gz", "song.tar_2.gz"},
		{"dot file", ".hidden", ".hidden_2"},
		{"trailing dot", "song.", "song_2."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			taken := filepath.Join(dir, tt.base)
			writeFile(t, taken, "x")

			got, err := uniquePath(taken)
			if err != nil {
				t.Fatalf("uniquePath: %v", err)
			}
			if got != filepath.Join(dir, tt.want) {
				t.Errorf("uniquePath(%q) = %q, want %q", tt.base, filepath.Base(got), tt.want)
			}
		})
	}
}

func TestUniquePathLeavesFreeNamesAlone(t *testing.T) {
	free := filepath.Join(t.TempDir(), "song.mp3")
	got, err := uniquePath(free)
	if err != nil {
		t.Fatalf("uniquePath: %v", err)
	}
	if got != free {
		t.Errorf("uniquePath(%q) = %q, want it unchanged", free, got)
	}
}

func TestMoveAudioFilesStaysInsideDestination(t *testing.T) {
	src := t.TempDir()
	root := t.TempDir()
	dst := filepath.Join(root, "music")
	writeFile(t, filepath.Join(src, "song.mp3"), "audio")

	// A subDirFunc is fed by file tags, so it is attacker-influenced input.
	// Containment must not depend on the caller sanitising it.
	moved, _, _, err := MoveAudioFiles(src, dst, func(string) string {
		return filepath.Join("..", "..", "etc")
	})
	if err != nil {
		t.Fatalf("MoveAudioFiles: %v", err)
	}
	if moved != 1 {
		t.Errorf("moved = %d, want 1", moved)
	}

	escaped := filepath.Join(filepath.Dir(root), "etc", "song.mp3")
	if _, err := os.Stat(escaped); err == nil {
		t.Errorf("file written outside the destination, at %s", escaped)
	}
	if _, err := os.Stat(filepath.Join(dst, "song.mp3")); err != nil {
		t.Errorf("file not kept inside the destination: %v", err)
	}
}

func TestCleanupRefusesSiblingOfTempDir(t *testing.T) {
	base := t.TempDir()
	tmp := filepath.Join(base, "tmp")
	sibling := filepath.Join(base, "tmpfoo") // shares the prefix, is not inside
	writeFile(t, filepath.Join(tmp, "keep"), "x")
	writeFile(t, filepath.Join(sibling, "keep"), "x")
	t.Setenv("TMPDIR", tmp)

	if err := Cleanup(sibling); err == nil {
		t.Error("expected an error: the directory only shares the prefix of the temp folder")
	}
	if _, err := os.Stat(sibling); err != nil {
		t.Errorf("sibling of the temp folder was deleted: %v", err)
	}
}

func TestCleanupRefusesTempDirItself(t *testing.T) {
	base := t.TempDir()
	tmp := filepath.Join(base, "tmp")
	writeFile(t, filepath.Join(tmp, "someone-elses-file"), "x")
	t.Setenv("TMPDIR", tmp)

	if err := Cleanup(tmp); err == nil {
		t.Error("expected an error: this would wipe the whole temp folder")
	}
	if _, err := os.Stat(filepath.Join(tmp, "someone-elses-file")); err != nil {
		t.Errorf("the temp folder was emptied: %v", err)
	}
}

func TestCleanupRefusesTraversalIntoTempDir(t *testing.T) {
	base := t.TempDir()
	tmp := filepath.Join(base, "tmp")
	outside := filepath.Join(base, "outside")
	writeFile(t, filepath.Join(tmp, "keep"), "x")
	writeFile(t, filepath.Join(outside, "keep"), "x")
	t.Setenv("TMPDIR", tmp)

	if err := Cleanup(filepath.Join(tmp, "..", "outside")); err == nil {
		t.Error("expected an error: the path climbs out of the temp folder")
	}
	if _, err := os.Stat(outside); err != nil {
		t.Errorf("directory outside the temp folder was deleted: %v", err)
	}
}

func TestMoveAudioFilesCountsFailedSidecars(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeFile(t, filepath.Join(src, "song.mp3"), "audio")
	writeFile(t, filepath.Join(src, "song.lrc"), "lyrics")

	// A directory sitting where the sidecar must land: the audio file moves,
	// its lyrics cannot follow.
	if err := os.MkdirAll(filepath.Join(dst, "song.lrc"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	moved, failed, lyricsFailed, err := MoveAudioFiles(src, dst, nil)
	if err != nil {
		t.Fatalf("MoveAudioFiles: %v", err)
	}
	if moved != 1 || failed != 0 {
		t.Errorf("moved=%d failed=%d, want 1 and 0", moved, failed)
	}
	if lyricsFailed != 1 {
		t.Errorf("lyricsFailed = %d, want 1: a sidecar left behind must not be silent", lyricsFailed)
	}
}

func TestNumberedNameStaysWithinTheNameLimit(t *testing.T) {
	// A name already at the filesystem limit: numbering it naively overflows.
	base := strings.Repeat("a", 251) + ".mp3"
	if len(base) != 255 {
		t.Fatalf("test fixture is %d bytes, want 255", len(base))
	}

	got := NumberedName(base, 2)
	if len(got) > 255 {
		t.Errorf("NumberedName produced %d bytes, want at most 255", len(got))
	}
	if filepath.Ext(got) != ".mp3" {
		t.Errorf("extension lost: %q", filepath.Ext(got))
	}
	if !strings.Contains(got, "_2") {
		t.Errorf("counter lost: %q", got[:20])
	}
}

func TestMoveAudioFilesKeepsAFileWhoseNameIsAtTheLimit(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	name := strings.Repeat("a", 251) + ".mp3"
	writeFile(t, filepath.Join(dst, name), "already here")
	writeFile(t, filepath.Join(src, name), "the new one")

	moved, failed, _, err := MoveAudioFiles(src, dst, nil)
	if err != nil {
		t.Fatalf("MoveAudioFiles: %v", err)
	}
	// Failing here means the new download is left in the temp directory, which
	// the caller then deletes: the file is lost, which is the opposite of what
	// the incremental suffix exists for.
	if moved != 1 || failed != 0 {
		t.Errorf("moved=%d failed=%d, want 1 and 0", moved, failed)
	}

	entries, err := os.ReadDir(dst)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("destination holds %d files, want 2", len(entries))
	}
}

// onlyOnPath replaces PATH with a directory holding a stub executable for each
// name, so a test controls exactly which tools exist.
func onlyOnPath(t *testing.T, names ...string) {
	t.Helper()
	dir := t.TempDir()
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
}

func TestCheckDependenciesNamesEveryMissingTool(t *testing.T) {
	onlyOnPath(t, "yt-dlp")

	err := CheckDependencies("yt-dlp", "ffmpeg", "fpcalc")
	if err == nil {
		t.Fatal("want an error for the missing tools, got none")
	}
	for _, missing := range []string{"ffmpeg", "fpcalc"} {
		if !strings.Contains(err.Error(), missing) {
			t.Errorf("error should name %s, got: %v", missing, err)
		}
	}
	if strings.Contains(err.Error(), "yt-dlp") {
		t.Errorf("error names yt-dlp, which is installed: %v", err)
	}
}

func TestCheckDependenciesPassesWhenAllPresent(t *testing.T) {
	onlyOnPath(t, "yt-dlp", "ffmpeg")

	if err := CheckDependencies("yt-dlp", "ffmpeg"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}
