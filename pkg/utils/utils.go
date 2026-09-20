package utils

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// Supported audio file extensions
var audioExtensions = map[string]bool{
	".mp3":  true,
	".m4a":  true,
	".flac": true,
	".opus": true,
	".wav":  true,
	".aac":  true,
	".ogg":  true,
}

// CheckDependencies verifies that required external commands are installed
func CheckDependencies() error {
	if _, err := exec.LookPath("yt-dlp"); err != nil {
		return fmt.Errorf("required command 'yt-dlp' not found in PATH. Install with: pip install yt-dlp")
	}

	return nil
}

// CreateTempDir creates a temporary folder for downloads
func CreateTempDir() (string, error) {
	dir, err := os.MkdirTemp("", "ytmusic-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temporary directory: %w", err)
	}
	return dir, nil
}

// Cleanup removes a temporary folder this program created.
//
// It deletes only directories strictly inside os.TempDir(). A prefix test is
// not enough: "/tmpfoo" starts with "/tmp" without being in it, and the temp
// folder itself passes such a test, which would hand os.RemoveAll everything
// every other program has left there.
func Cleanup(dir string) error {
	if dir == "" {
		return nil
	}

	rel, err := filepath.Rel(os.TempDir(), filepath.Clean(dir))
	// "." is the temp folder itself, ".." and "../…" are outside it.
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("refusing to delete directory outside temp folder: %s", dir)
	}

	return os.RemoveAll(dir)
}

// FindAudioFiles recursively finds all audio files in a directory.
func FindAudioFiles(dir string) ([]string, error) {
	if dir == "" {
		return nil, fmt.Errorf("directory path cannot be empty")
	}

	if _, err := os.Stat(dir); err != nil {
		return nil, fmt.Errorf("directory does not exist: %s", dir)
	}

	var files []string

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}

		if !info.IsDir() && audioExtensions[strings.ToLower(filepath.Ext(path))] {
			files = append(files, path)
		}
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("error walking directory %s: %w", dir, err)
	}

	return files, nil
}

// MoveAudioFiles finds all audio files in srcDir and moves them to dstDir.
// If subDirFunc is provided, it is called for each file to determine a subdirectory
// within dstDir (e.g. "Artist/Album"). If it returns "", the file is placed in dstDir directly.
// Returns the number of files moved and the number of failures.
func MoveAudioFiles(srcDir, dstDir string, subDirFunc func(string) string) (moved int, failed int, err error) {
	if err := os.MkdirAll(dstDir, 0755); err != nil {
		return 0, 0, fmt.Errorf("failed to create output directory: %w", err)
	}

	files, err := FindAudioFiles(srcDir)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to find audio files: %w", err)
	}

	for _, file := range files {
		destDir := dstDir
		if subDirFunc != nil {
			// subDirFunc is fed by file tags, so it carries attacker-influenced
			// input. A subdirectory that climbs out of dstDir is dropped, not
			// followed: this holds even if the caller's own sanitising has a
			// hole we did not foresee.
			if sub := subDirFunc(file); sub != "" && within(dstDir, filepath.Join(dstDir, sub)) {
				destDir = filepath.Join(dstDir, sub)
			}
		}

		dst, uniqueErr := uniquePath(filepath.Join(destDir, filepath.Base(file)))
		if uniqueErr != nil {
			failed++
			continue
		}
		if moveErr := MoveFile(file, dst); moveErr != nil {
			failed++
			continue
		}
		moved++

		// The sidecar follows the name the audio file actually got: keeping
		// the original one would leave lyrics no player can pair with a track.
		lrcSrc := strings.TrimSuffix(file, filepath.Ext(file)) + ".lrc"
		if _, err := os.Stat(lrcSrc); err == nil {
			lrcDst := strings.TrimSuffix(dst, filepath.Ext(dst)) + ".lrc"
			MoveFile(lrcSrc, lrcDst)
		}
	}

	return moved, failed, nil
}

// within reports whether path stays inside root, root itself included.
func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

// NumberedName returns the n-th candidate for a file name: base itself for
// n == 1, then "name_2.ext", "name_3.ext", … The extension stays last so the
// file keeps being recognised as audio.
func NumberedName(base string, n int) string {
	if n <= 1 {
		return base
	}
	ext := filepath.Ext(base)
	if ext == base {
		ext = "" // a dotfile like ".hidden" is all name, no extension
	}
	return fmt.Sprintf("%s_%d%s", base[:len(base)-len(ext)], n, ext)
}

// uniquePath returns dst when nothing is there, otherwise dst with an
// incremental suffix ("name_2.mp3", "name_3.mp3", …).
//
// The check is stat-then-rename, so it assumes no other process writes into
// the same directory at the same moment. That holds today: the move phase is
// sequential and single-process. If it ever stops holding, reserve the name
// with O_CREATE|O_EXCL instead of testing for it.
func uniquePath(dst string) (string, error) {
	dir, base := filepath.Split(dst)
	for n := 1; ; n++ {
		candidate := filepath.Join(dir, NumberedName(base, n))
		// Lstat, so a dangling symlink still counts as taken.
		if _, err := os.Lstat(candidate); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return candidate, nil
			}
			return "", fmt.Errorf("failed to check %s: %w", candidate, err)
		}
	}
}

// MoveFile moves a file from src to dst, creating the destination directory if needed.
// Falls back to copy+delete when src and dst are on different filesystems.
func MoveFile(src, dst string) error {
	if src == "" || dst == "" {
		return fmt.Errorf("source and destination paths cannot be empty")
	}

	if _, err := os.Stat(src); err != nil {
		return fmt.Errorf("source file does not exist: %s", src)
	}

	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return fmt.Errorf("failed to create destination directory: %w", err)
	}

	if err := os.Rename(src, dst); err != nil {
		// Cross-device link: fall back to copy + delete
		var linkErr *os.LinkError
		if errors.As(err, &linkErr) && errors.Is(linkErr.Err, syscall.EXDEV) {
			return copyAndDelete(src, dst)
		}
		return fmt.Errorf("failed to move %s to %s: %w", src, dst, err)
	}

	return nil
}

func copyAndDelete(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("failed to open source %s: %w", src, err)
	}
	defer srcFile.Close()

	srcInfo, err := srcFile.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat source %s: %w", src, err)
	}

	dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, srcInfo.Mode())
	if err != nil {
		return fmt.Errorf("failed to create destination %s: %w", dst, err)
	}
	defer dstFile.Close()

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		os.Remove(dst)
		return fmt.Errorf("failed to copy %s to %s: %w", src, dst, err)
	}

	if err := dstFile.Close(); err != nil {
		os.Remove(dst)
		return fmt.Errorf("failed to close destination %s: %w", dst, err)
	}

	return os.Remove(src)
}
