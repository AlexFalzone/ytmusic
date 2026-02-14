package metadata

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"go.senan.xyz/taglib"
)

// WriteTags writes the given TrackInfo metadata to an audio file.
func WriteTags(path string, info TrackInfo) error {
	tags := make(map[string][]string)

	if info.Title != "" {
		tags[taglib.Title] = []string{info.Title}
	}
	if info.Artist != "" {
		tags[taglib.Artist] = []string{info.Artist}
	}
	if info.Album != "" {
		tags[taglib.Album] = []string{info.Album}
	}
	if info.AlbumArtist != "" {
		tags[taglib.AlbumArtist] = []string{info.AlbumArtist}
	}
	if info.TrackNumber > 0 {
		tags[taglib.TrackNumber] = []string{strconv.Itoa(info.TrackNumber)}
	}
	if info.DiscNumber > 0 {
		tags[taglib.DiscNumber] = []string{strconv.Itoa(info.DiscNumber)}
	}
	if info.ReleaseDate != "" {
		tags[taglib.Date] = []string{info.ReleaseDate}
	} else if info.Year > 0 {
		tags[taglib.Date] = []string{strconv.Itoa(info.Year)}
	}
	if info.Genre != "" {
		tags[taglib.Genre] = []string{info.Genre}
	}
	if info.ISRC != "" {
		tags[taglib.ISRC] = []string{info.ISRC}
	}

	if err := taglib.WriteTags(path, tags, 0); err != nil {
		return fmt.Errorf("failed to write tags to %s: %w", path, err)
	}
	return nil
}

// SubDirFromTags reads an audio file's tags and returns an "Artist/Album"
// subdirectory path for organizing files. Returns "" if tags can't be read.
func SubDirFromTags(path string) string {
	tags, err := taglib.ReadTags(path)
	if err != nil {
		return ""
	}

	artist := firstTag(tags, taglib.AlbumArtist)
	if artist == "" {
		artist = firstTag(tags, taglib.Artist)
		if i := strings.Index(artist, ","); i > 0 {
			artist = strings.TrimSpace(artist[:i])
		}
	}
	album := firstTag(tags, taglib.Album)

	if artist == "" {
		artist = "Unknown Artist"
	}
	if album == "" {
		album = "Unknown Album"
	}

	return filepath.Join(sanitizePath(artist), sanitizePath(album))
}

// sanitizePath removes or replaces characters that are problematic in file paths.
func sanitizePath(s string) string {
	s = strings.TrimSpace(s)
	replacer := strings.NewReplacer(
		"/", "_",
		"\\", "_",
		":", "_",
		"*", "_",
		"?", "_",
		"\"", "_",
		"<", "_",
		">", "_",
		"|", "_",
	)
	return replacer.Replace(s)
}

// WriteArtwork embeds artwork image data into an audio file.
func WriteArtwork(path string, imageData []byte) error {
	if len(imageData) == 0 {
		return nil
	}
	if err := taglib.WriteImage(path, imageData); err != nil {
		return fmt.Errorf("failed to write artwork to %s: %w", path, err)
	}
	return nil
}
