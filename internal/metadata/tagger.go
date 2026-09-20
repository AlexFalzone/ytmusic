package metadata

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

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
	if artist == "" || strings.EqualFold(artist, "Various Artists") {
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

// maxComponentBytes is the length limit a single path component gets on ext4,
// APFS and NTFS alike.
const maxComponentBytes = 255

// windowsReserved are device names that cannot be a path component on Windows
// or on an SMB share, whatever extension follows them.
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

// sanitizePath turns a tag value into one safe path component. Beyond the
// characters filesystems reject, it defuses the values that are a path
// instruction rather than a name — "." and ".." — and the ones a filesystem
// would rewrite or refuse behind our back.
func sanitizePath(s string) string {
	s = pathReplacer.Replace(s)

	// Control characters give unreadable names, and a NUL byte ends the path
	// early at the syscall boundary — the rest of the name silently vanishes.
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)

	// Stripping leading and trailing dots and spaces is what disarms "." and
	// "..", and Windows drops them from names anyway.
	s = strings.Trim(s, " .")

	if len(s) > maxComponentBytes {
		s = truncateBytes(s, maxComponentBytes)
		s = strings.Trim(s, " .")
	}

	// Never return "": it would collapse in filepath.Join and move the file a
	// level up, straight out of its album directory.
	if s == "" {
		return "_"
	}

	if windowsReserved[strings.ToUpper(s)] {
		return s + "_"
	}
	return s
}

// truncateBytes cuts s to at most max bytes without splitting a rune: half a
// multi-byte character is not a valid name.
func truncateBytes(s string, max int) string {
	var b strings.Builder
	for _, r := range s {
		if b.Len()+utf8.RuneLen(r) > max {
			break
		}
		b.WriteRune(r)
	}
	return b.String()
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
