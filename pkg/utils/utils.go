package utils

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// CheckDependencies verifies that required external commands are installed
func CheckDependencies() error {
	if _, err := exec.LookPath("yt-dlp"); err != nil {
		return fmt.Errorf("required command 'yt-dlp' not found in PATH. Install with: pip install yt-dlp")
	}

	cmd := exec.Command("python3", "-m", "beets", "version")
	cmd.Stderr = nil
	cmd.Stdout = nil
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("beets not found (tried: python3 -m beets). Install with: pip install beets")
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

// Cleanup removes the temporary folder.
// Safety check: only deletes directories in /tmp
func Cleanup(dir string) error {
	if dir == "" {
		return nil
	}

	if !filepath.HasPrefix(dir, os.TempDir()) {
		return fmt.Errorf("refusing to delete directory outside temp folder: %s", dir)
	}

	return os.RemoveAll(dir)
}

// FindMP3Files recursively finds all MP3 files in a directory
func FindMP3Files(dir string) ([]string, error) {
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

		if !info.IsDir() && filepath.Ext(path) == ".mp3" {
			files = append(files, path)
		}
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("error walking directory %s: %w", dir, err)
	}

	return files, nil
}

// MoveFile moves a file from src to dst, creating the destination directory if needed
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
