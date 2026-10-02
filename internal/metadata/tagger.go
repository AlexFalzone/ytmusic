package metadata

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"ytmusic/pkg/utils"

	"go.senan.xyz/taglib"
)

func tagMap(info TrackInfo) map[string][]string {
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
	return tags
}

// "Artist/Album", or "" when the tags cannot be read.
func SubDirFromTags(path string) string {
	tags, err := taglib.ReadTags(path)
	if err != nil {
		return ""
	}

	artist := FirstTag(tags, taglib.AlbumArtist)
	if artist == "" || strings.EqualFold(artist, "Various Artists") {
		artist = primaryArtist(FirstTag(tags, taglib.Artist))
	}
	album := FirstTag(tags, taglib.Album)

	if artist == "" {
		artist = "Unknown Artist"
	}
	if album == "" {
		album = "Unknown Album"
	}

	return filepath.Join(sanitizePath(artist), sanitizePath(album))
}

const maxComponentBytes = utils.MaxNameBytes

// Device names Windows and SMB shares refuse as a path component, whatever the extension.
var windowsReserved = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true,
	"COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true,
	"LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

var pathReplacer = strings.NewReplacer(
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

func sanitizePath(s string) string {
	s = pathReplacer.Replace(s)

	// A NUL byte would silently cut the path short at the syscall boundary.
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)

	// Disarms "." and "..".
	s = strings.Trim(s, " .")

	if len(s) > maxComponentBytes {
		s = utils.TruncateBytes(s, maxComponentBytes)
		s = strings.Trim(s, " .")
	}

	// "" would collapse in filepath.Join and move the file out of its album directory.
	if s == "" {
		return "_"
	}

	stem, _, _ := strings.Cut(s, ".")
	if windowsReserved[strings.ToUpper(stem)] {
		return s + "_"
	}
	return s
}

func WriteArtwork(path string, imageData []byte) error {
	if len(imageData) == 0 {
		return nil
	}
	if err := taglib.WriteImage(path, imageData); err != nil {
		return fmt.Errorf("failed to write artwork to %s: %w", path, err)
	}
	return nil
}

// Without an album artist, Navidrome files each featured-artist track under its own entry.
func albumArtistFallback(existing map[string][]string, artist string) string {
	if FirstTag(existing, taglib.AlbumArtist) != "" {
		return ""
	}
	if artist == "" {
		artist = FirstTag(existing, taglib.Artist)
	}
	return primaryArtist(artist)
}

func primaryArtist(artist string) string {
	if i := strings.Index(artist, ","); i > 0 {
		return strings.TrimSpace(artist[:i])
	}
	return artist
}

// The file's track and disc numbers win: a provider may have picked the wrong release.
func mergeWithExisting(tags map[string][]string, info TrackInfo) TrackInfo {
	if n := parseTagInt(tags, taglib.TrackNumber); n > 0 {
		info.TrackNumber = n
	}
	if n := parseTagInt(tags, taglib.DiscNumber); n > 0 {
		info.DiscNumber = n
	}
	return info
}

func parseTagInt(tags map[string][]string, key string) int {
	s := FirstTag(tags, key)
	if s == "" {
		return 0
	}
	if i := strings.Index(s, "/"); i > 0 { // "5/12"
		s = strings.TrimSpace(s[:i])
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

func FirstTag(tags map[string][]string, key string) string {
	if vals, ok := tags[key]; ok && len(vals) > 0 {
		return vals[0]
	}
	return ""
}
