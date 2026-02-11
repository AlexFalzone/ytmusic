package metadata

import (
	"regexp"
	"strings"
)

// Patterns to remove from YouTube titles
var titleCleanupPatterns = []*regexp.Regexp{
	// Parenthesized suffixes
	regexp.MustCompile(`(?i)\s*\(official\s+(music\s+)?video\)`),
	regexp.MustCompile(`(?i)\s*\(official\s+audio\)`),
	regexp.MustCompile(`(?i)\s*\(official\s+lyric\s+video\)`),
	regexp.MustCompile(`(?i)\s*\(official\s+visualizer\)`),
	regexp.MustCompile(`(?i)\s*\(lyrics?\)`),
	regexp.MustCompile(`(?i)\s*\(visual(?:izer)?\)`),
	regexp.MustCompile(`(?i)\s*\(audio\)`),
	regexp.MustCompile(`(?i)\s*\(hd\)`),
	regexp.MustCompile(`(?i)\s*\(hq\)`),
	regexp.MustCompile(`(?i)\s*\(4k\)`),
	regexp.MustCompile(`(?i)\s*\(explicit\)`),
	regexp.MustCompile(`(?i)\s*\(clean\)`),

	// Bracketed suffixes
	regexp.MustCompile(`(?i)\s*\[official\s+(music\s+)?video\]`),
	regexp.MustCompile(`(?i)\s*\[official\s+audio\]`),
	regexp.MustCompile(`(?i)\s*\[official\s+lyric\s+video\]`),
	regexp.MustCompile(`(?i)\s*\[official\s+visualizer\]`),
	regexp.MustCompile(`(?i)\s*\[lyrics?\]`),
	regexp.MustCompile(`(?i)\s*\[visual(?:izer)?\]`),
	regexp.MustCompile(`(?i)\s*\[audio\]`),
	regexp.MustCompile(`(?i)\s*\[hd\]`),
	regexp.MustCompile(`(?i)\s*\[hq\]`),
	regexp.MustCompile(`(?i)\s*\[4k\]`),
	regexp.MustCompile(`(?i)\s*\[explicit\]`),
	regexp.MustCompile(`(?i)\s*\[clean\]`),
}

// Patterns to extract featuring artists from the title
var featuringPattern = regexp.MustCompile(`(?i)\s*[\(\[]\s*(?:feat\.?|ft\.?|featuring)\s+([^\)\]]+)[\)\]]`)

// Pattern to detect "VEVO" channel suffix in artist name
var vevoPattern = regexp.MustCompile(`(?i)vevo$`)

// Pattern for "Artist - Title" format (common in YouTube titles)
var artistTitleSeparator = regexp.MustCompile(`^(.+?)\s*[-–—]\s*(.+)$`)

// NormalizeQuery takes raw metadata (typically from yt-dlp) and returns a cleaned SearchQuery.
func NormalizeQuery(title, artist string) SearchQuery {
	title = strings.TrimSpace(title)
	artist = strings.TrimSpace(artist)

	// Clean VEVO suffix from artist
	artist = vevoPattern.ReplaceAllString(artist, "")
	artist = strings.TrimSpace(artist)

	// If we have no title but have artist, nothing useful to search
	if title == "" {
		return SearchQuery{Title: title, Artist: artist}
	}

	// Remove YouTube-specific suffixes from title
	for _, p := range titleCleanupPatterns {
		title = p.ReplaceAllString(title, "")
	}

	// Extract featuring artists (keep them stripped from title for cleaner search)
	title = featuringPattern.ReplaceAllString(title, "")

	// If artist is empty, try to split "Artist - Title" from the title string
	if artist == "" {
		if m := artistTitleSeparator.FindStringSubmatch(title); m != nil {
			artist = strings.TrimSpace(m[1])
			title = strings.TrimSpace(m[2])
		}
	}

	title = strings.TrimSpace(title)
	artist = strings.TrimSpace(artist)

	return SearchQuery{
		Title:  title,
		Artist: artist,
	}
}
