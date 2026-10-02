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
	"unicode/utf8"
)

var audioExtensions = map[string]bool{
	".mp3":  true,
	".m4a":  true,
	".flac": true,
	".opus": true,
	".wav":  true,
	".aac":  true,
	".ogg":  true,
}

var installHints = map[string]string{
	"yt-dlp":  "install it with: pip install yt-dlp",
	"ffmpeg":  "install FFmpeg",
	"ffprobe": "install FFmpeg, which ships ffprobe",
	"fpcalc":  "install Chromaprint, or remove acoustid_api_key from the config to run without fingerprinting",
}

// All missing programs in one error, so one run tells the user everything to install.
func CheckDependencies(names ...string) error {
	var missing []error
	for _, name := range names {
		if _, err := exec.LookPath(name); err != nil {
			missing = append(missing, fmt.Errorf("required program %q not found in PATH: %s", name, installHints[name]))
		}
	}
	return errors.Join(missing...)
}

func CreateTempDir() (string, error) {
	dir, err := os.MkdirTemp("", "ytmusic-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temporary directory: %w", err)
	}
	return dir, nil
}

// Only strictly inside os.TempDir(): a prefix test would accept "/tmpfoo" and /tmp itself.
func Cleanup(dir string) error {
	if dir == "" {
		return nil
	}

	rel, err := filepath.Rel(os.TempDir(), filepath.Clean(dir))
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("refusing to delete directory outside temp folder: %s", dir)
	}

	return os.RemoveAll(dir)
}

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

// subDirFunc may return "Artist/Album", or "" for dstDir itself.
func MoveAudioFiles(srcDir, dstDir string, subDirFunc func(string) string) (moved int, failed int, lyricsFailed int, err error) {
	if err := os.MkdirAll(dstDir, 0755); err != nil {
		return 0, 0, 0, fmt.Errorf("failed to create output directory: %w", err)
	}

	files, err := FindAudioFiles(srcDir)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("failed to find audio files: %w", err)
	}

	for _, file := range files {
		destDir := dstDir
		if subDirFunc != nil {
			// Fed by file tags: escaping dstDir is refused whatever the caller sanitised.
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

		// The sidecar follows the name the audio file actually got.
		lrcSrc := strings.TrimSuffix(file, filepath.Ext(file)) + ".lrc"
		if _, err := os.Stat(lrcSrc); err == nil {
			lrcDst := strings.TrimSuffix(dst, filepath.Ext(dst)) + ".lrc"
			if lrcErr := MoveFile(lrcSrc, lrcDst); lrcErr != nil {
				lyricsFailed++
			}
		}
	}

	return moved, failed, lyricsFailed, nil
}

func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

// Per name component, on ext4, APFS and NTFS alike.
const MaxNameBytes = 255

// Never splits a rune.
func TruncateBytes(s string, max int) string {
	var b strings.Builder
	for _, r := range s {
		if b.Len()+utf8.RuneLen(r) > max {
			break
		}
		b.WriteRune(r)
	}
	return b.String()
}

// base for n == 1, then "name_2.ext": the extension stays last so the file is still recognised as audio.
func NumberedName(base string, n int) string {
	if n <= 1 {
		return base
	}
	ext := filepath.Ext(base)
	if ext == base {
		ext = "" // a dotfile like ".hidden" is all name, no extension
	}
	stem := base[:len(base)-len(ext)]
	counter := fmt.Sprintf("_%d", n)

	// A name at the limit would overflow once numbered: shorten the stem instead.
	if over := len(stem) + len(counter) + len(ext) - MaxNameBytes; over > 0 {
		stem = TruncateBytes(stem, len(stem)-over)
	}
	return stem + counter + ext
}

// Stat-then-rename: correct only while the move phase is sequential and single-process.
func uniquePath(dst string) (string, error) {
	dir, base := filepath.Split(dst)
	for n := 1; ; n++ {
		candidate := filepath.Join(dir, NumberedName(base, n))
		// Lstat: a dangling symlink still counts as taken.
		if _, err := os.Lstat(candidate); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return candidate, nil
			}
			return "", fmt.Errorf("failed to check %s: %w", candidate, err)
		}
	}
}

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
	defer func() { _ = srcFile.Close() }() // only read from

	srcInfo, err := srcFile.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat source %s: %w", src, err)
	}

	dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, srcInfo.Mode())
	if err != nil {
		return fmt.Errorf("failed to create destination %s: %w", dst, err)
	}

	// On a written file, Close can be what reports the data never reached the disk.
	_, copyErr := io.Copy(dstFile, srcFile)
	if err := errors.Join(copyErr, dstFile.Close()); err != nil {
		// A partial copy left in the library would pass for the real track.
		if rmErr := os.Remove(dst); rmErr != nil {
			err = errors.Join(err, fmt.Errorf("removing the partial copy: %w", rmErr))
		}
		return fmt.Errorf("failed to copy %s to %s: %w", src, dst, err)
	}

	return os.Remove(src)
}
