package utils

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
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

	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}

		if !d.IsDir() && audioExtensions[strings.ToLower(filepath.Ext(path))] {
			files = append(files, path)
		}
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("error walking directory %s: %w", dir, err)
	}

	return files, nil
}

// What a move left undone. Each error names its file, which stays where it was.
type MoveReport struct {
	Moved        int
	Failed       []error
	LyricsFailed []error
}

// subDirFunc may return "Artist/Album", or "" for dstDir itself.
func MoveAudioFiles(srcDir, dstDir string, subDirFunc func(string) string) (MoveReport, error) {
	var report MoveReport
	if err := os.MkdirAll(dstDir, 0755); err != nil {
		return report, fmt.Errorf("failed to create output directory: %w", err)
	}

	files, err := FindAudioFiles(srcDir)
	if err != nil {
		return report, fmt.Errorf("failed to find audio files: %w", err)
	}

	for _, file := range files {
		destDir := dstDir
		if subDirFunc != nil {
			// Fed by file tags: escaping dstDir is refused whatever the caller sanitised.
			if sub := subDirFunc(file); sub != "" && within(dstDir, filepath.Join(dstDir, sub)) {
				destDir = filepath.Join(dstDir, sub)
			}
		}

		dst, err := moveToLibrary(file, filepath.Join(destDir, filepath.Base(file)))
		if err != nil {
			report.Failed = append(report.Failed, err)
			continue
		}
		report.Moved++

		// The sidecar follows the name the audio file actually got.
		lrcSrc := strings.TrimSuffix(file, filepath.Ext(file)) + ".lrc"
		if _, err := os.Stat(lrcSrc); err == nil {
			lrcDst := strings.TrimSuffix(dst, filepath.Ext(dst)) + ".lrc"
			if err := moveExact(lrcSrc, lrcDst); err != nil {
				report.LyricsFailed = append(report.LyricsFailed, err)
			}
		}
	}

	return report, nil
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

// A var so tests can put the library on another filesystem.
var rename = os.Rename

const stageSuffix = ".part"

type stagedFile struct {
	path    string // hidden, next to the destination
	src     string
	renamed bool // src is gone; otherwise it goes once the file is published
}

// Complete before it has a visible name: a kill during the copy leaves a hidden .part, never a truncated track.
func stage(src, dir string) (stagedFile, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return stagedFile{}, fmt.Errorf("failed to create destination directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".ytmusic-*"+stageSuffix)
	if err != nil {
		return stagedFile{}, fmt.Errorf("staging %s: %w", src, err)
	}

	err = rename(src, tmp.Name())
	if err == nil {
		_ = tmp.Close() // the empty file the rename replaced: nothing was written through it
		return stagedFile{path: tmp.Name(), src: src, renamed: true}, nil
	}
	if errors.Is(err, syscall.EXDEV) {
		err = copyInto(tmp, src)
	}
	if err := errors.Join(err, tmp.Close()); err != nil {
		return stagedFile{}, fmt.Errorf("staging %s: %w", src, errors.Join(err, os.Remove(tmp.Name())))
	}
	return stagedFile{path: tmp.Name(), src: src}, nil
}

func copyInto(dst *os.File, src string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }() // only read from

	info, err := in.Stat()
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, in); err != nil {
		return err
	}
	if err := dst.Chmod(info.Mode()); err != nil {
		return err
	}
	// The source goes next: a write the disk refuses has to show up before that.
	return dst.Sync()
}

// O_EXCL takes the name atomically, whoever else is moving files here, and the rename replaces the empty claim.
// fs.ErrExist: the name is taken.
func (f stagedFile) publish(dst string) error {
	claim, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	if err := claim.Close(); err != nil {
		return errors.Join(err, os.Remove(dst))
	}
	if err := rename(f.path, dst); err != nil {
		return errors.Join(err, os.Remove(dst))
	}
	if !f.renamed {
		_ = os.Remove(f.src) // the file is in the library; a leftover source dies with the temp dir
	}
	return nil
}

// A renamed source goes back, so a failed move leaves the file where it was.
func (f stagedFile) abort() error {
	if f.renamed {
		return rename(f.path, f.src)
	}
	return os.Remove(f.path)
}

// To dst, or the first free "name_2.ext": nothing in the library is ever overwritten.
func moveToLibrary(src, dst string) (string, error) {
	dir, base := filepath.Split(dst)
	f, err := stage(src, dir)
	if err != nil {
		return "", err
	}
	for n := 1; ; n++ {
		candidate := filepath.Join(dir, NumberedName(base, n))
		err := f.publish(candidate)
		if err == nil {
			return candidate, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return "", errors.Join(fmt.Errorf("moving %s to %s: %w", src, candidate, err), f.abort())
		}
	}
}

// Like moveToLibrary for a file that must keep its name: a taken name is a failure.
func moveExact(src, dst string) error {
	f, err := stage(src, filepath.Dir(dst))
	if err != nil {
		return err
	}
	if err := f.publish(dst); err != nil {
		return errors.Join(fmt.Errorf("moving %s to %s: %w", src, dst, err), f.abort())
	}
	return nil
}

// Within one filesystem, and over whatever dst already is: not for the library.
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
		return fmt.Errorf("failed to move %s to %s: %w", src, dst, err)
	}

	return nil
}
