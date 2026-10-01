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

// installHints say how to get each external program the pipeline runs.
var installHints = map[string]string{
	"yt-dlp":  "install it with: pip install yt-dlp",
	"ffmpeg":  "install FFmpeg",
	"ffprobe": "install FFmpeg, which ships ffprobe",
	"fpcalc":  "install Chromaprint, or remove acoustid_api_key from the config to run without fingerprinting",
}

// CheckDependencies reports every program in names that is not on PATH, all
// in one error, so a single run tells the user everything there is to install.
func CheckDependencies(names ...string) error {
	var missing []error
	for _, name := range names {
		if _, err := exec.LookPath(name); err != nil {
			missing = append(missing, fmt.Errorf("required program %q not found in PATH: %s", name, installHints[name]))
		}
	}
	return errors.Join(missing...)
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
// Returns how many files moved, how many failed, and how many .lrc sidecars
// could not follow their track. The sidecar count is reported rather than
// logged: this package has no logger, and its caller does.
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
			if lrcErr := MoveFile(lrcSrc, lrcDst); lrcErr != nil {
				lyricsFailed++
			}
		}
	}

	return moved, failed, lyricsFailed, nil
}

// within reports whether path stays inside root, root itself included.
func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

// MaxNameBytes is the length limit a single file or directory name gets on
// ext4, APFS and NTFS alike.
const MaxNameBytes = 255

// TruncateBytes cuts s to at most max bytes without splitting a rune: half a
// multi-byte character is not a valid name.
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
	stem := base[:len(base)-len(ext)]
	counter := fmt.Sprintf("_%d", n)

	// A name already at the filesystem limit overflows once numbered, and the
	// move then fails with ENAMETOOLONG — losing the very file the suffix
	// exists to protect. Shorten the stem instead.
	if over := len(stem) + len(counter) + len(ext) - MaxNameBytes; over > 0 {
		stem = TruncateBytes(stem, len(stem)-over)
	}
	return stem + counter + ext
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
	// Only read from: a failed close loses nothing.
	defer func() { _ = srcFile.Close() }()

	srcInfo, err := srcFile.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat source %s: %w", src, err)
	}

	dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, srcInfo.Mode())
	if err != nil {
		return fmt.Errorf("failed to create destination %s: %w", dst, err)
	}

	// The close is part of the copy: on a written file it can be the call that
	// reports the data did not make it to disk.
	_, copyErr := io.Copy(dstFile, srcFile)
	if err := errors.Join(copyErr, dstFile.Close()); err != nil {
		// A partial copy left in the library would pass for the real track, so
		// failing to remove it has to be reported too.
		if rmErr := os.Remove(dst); rmErr != nil {
			err = errors.Join(err, fmt.Errorf("removing the partial copy: %w", rmErr))
		}
		return fmt.Errorf("failed to copy %s to %s: %w", src, dst, err)
	}

	return os.Remove(src)
}
