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
var featuringPattern = regexp.MustCompile(`(?i)\s*[\(\[]\s*(?:feat\.?|ft\.?|featuring|with)\s+([^\)\]]+)[\)\]]`)

// trailingFeaturingPattern catches a credit left outside parentheses, as in
// "Song ft. Someone". "with" stays out: unparenthesised, it belongs to titles
// like "Stay With Me".
var trailingFeaturingPattern = regexp.MustCompile(`(?i)\s+(?:feat\.?|ft\.?|featuring)\s+.+$`)

// remasterPattern matches remaster notes. A remaster is the same recording, so
// the note is noise, not a variant: treating it as one would reject the many
// albums only available remastered.
var remasterPattern = regexp.MustCompile(`(?i)^(\d{4}\s+)?(digital(ly)?\s+)?remaster(ed)?(\s+\d{4})?(\s+version)?$`)

// segmentPattern matches one parenthesised or bracketed segment.
var segmentPattern = regexp.MustCompile(`\s*[\(\[]([^\(\)\[\]]*)[\)\]]`)

// dashSuffixPattern splits off the last " - " suffix, where Spotify writes
// "Remastered 2011" and "Live". The spaces are required so that hyphenated
// words such as "Anti-Hero" are never cut.
var dashSuffixPattern = regexp.MustCompile(`^(.*\S)\s+[-–—]\s+(.+)$`)

// Pattern to detect "VEVO" channel suffix in artist name
var vevoPattern = regexp.MustCompile(`(?i)vevo$`)

// Pattern for "Artist - Title" format (common in YouTube titles)
var artistTitleSeparator = regexp.MustCompile(`^(.+?)\s*[-–—]\s*(.+)$`)

// NormalizeQuery turns raw metadata (typically from yt-dlp) into the query sent
// to providers, plus the variant the title declares. The query carries the
// song's name only: a variant such as "Sped Up" is usually a fan edit no
// provider distributes, so searching for it would find nothing — not even the
// original to borrow metadata from.
func NormalizeQuery(title, artist string) (SearchQuery, Version) {
	title = strings.TrimSpace(title)
	artist = strings.TrimSpace(vevoPattern.ReplaceAllString(strings.TrimSpace(artist), ""))

	if title == "" {
		return SearchQuery{Artist: artist}, Version{}
	}

	// With no artist tag, "Artist - Song - Live" starts with the artist: split
	// it off first, so that dash is not mistaken for a variant suffix.
	if artist == "" {
		if m := artistTitleSeparator.FindStringSubmatch(stripNoise(title)); m != nil {
			artist = strings.TrimSpace(m[1])
			title = strings.TrimSpace(m[2])
		}
	}

	base, version := cleanTitle(title)
	return SearchQuery{Title: base, Artist: artist}, version
}

// cleanTitle reduces a title to the song's name and the variant it declares.
// It is applied to the query and to every candidate alike, so both sides are
// compared on the same terms.
func cleanTitle(raw string) (string, Version) {
	title := stripNoise(raw)

	var keys, labels []string
	title = segmentPattern.ReplaceAllStringFunc(title, func(segment string) string {
		inner := strings.TrimSpace(segmentPattern.FindStringSubmatch(segment)[1])
		if key, ok := versionKey(inner); ok {
			keys = append(keys, key)
			labels = append(labels, inner)
			return ""
		}
		if remasterPattern.MatchString(inner) {
			return ""
		}
		return segment
	})

	if m := dashSuffixPattern.FindStringSubmatch(title); m != nil {
		suffix := strings.TrimSpace(m[2])
		if key, ok := versionKey(suffix); ok {
			keys = append(keys, key)
			labels = append(labels, suffix)
			title = m[1]
		} else if remasterPattern.MatchString(suffix) {
			title = m[1]
		}
	}

	title = trailingFeaturingPattern.ReplaceAllString(title, "")
	return strings.Join(strings.Fields(title), " "), newVersion(keys, labels)
}

// stripNoise removes what never carries meaning: promotional suffixes and
// parenthesised featuring credits.
func stripNoise(title string) string {
	for _, p := range titleCleanupPatterns {
		title = p.ReplaceAllString(title, "")
	}
	return featuringPattern.ReplaceAllString(title, "")
}
