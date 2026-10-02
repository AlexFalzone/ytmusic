package metadata

import (
	"regexp"
	"strings"
)

var promoPattern = func() *regexp.Regexp {
	inner := `official\s+(?:music\s+|lyric\s+)?video|official\s+(?:audio|visualizer)|lyrics?|visual(?:izer)?|audio|hd|hq|4k|explicit|clean`
	return regexp.MustCompile(`(?i)\s*(?:\((?:` + inner + `)\)|\[(?:` + inner + `)\])`)
}()

var featuringPattern = regexp.MustCompile(`(?i)\s*[\(\[]\s*(?:feat\.?|ft\.?|featuring|with)\s+([^\)\]]+)[\)\]]`)

// No "with": unparenthesised, it belongs to titles like "Stay With Me".
var trailingFeaturingPattern = regexp.MustCompile(`(?i)\s+(?:feat\.?|ft\.?|featuring)\s+.+$`)

// A remaster is the same recording, not a variant: many albums exist only remastered.
var remasterPattern = regexp.MustCompile(`(?i)^(\d{4}\s+)?(digital(ly)?\s+)?remaster(ed)?(\s+\d{4})?(\s+version)?$`)

var segmentPattern = regexp.MustCompile(`\s*[\(\[]([^\(\)\[\]]*)[\)\]]`)

// Spotify writes "- Remastered 2011". The spaces keep "Anti-Hero" whole.
var dashSuffixPattern = regexp.MustCompile(`^(.*\S)\s+[-–—]\s+(.+)$`)

var vevoPattern = regexp.MustCompile(`(?i)vevo$`)

// The spaces keep "Anti-Hero" from splitting into artist "Anti", title "Hero".
var artistTitleSeparator = regexp.MustCompile(`^(.+?)\s+[-–—]\s+(.+)$`)

// Query by the song's name only: a variant like "Sped Up" is usually a fan edit no provider has.
func NormalizeQuery(title, artist string) (SearchQuery, Version) {
	title = strings.TrimSpace(title)
	artist = strings.TrimSpace(vevoPattern.ReplaceAllString(strings.TrimSpace(artist), ""))

	if title == "" {
		return SearchQuery{Artist: artist}, Version{}
	}

	// "Artist - Song - Live": the first dash is the artist, not a variant suffix.
	if artist == "" {
		if m := artistTitleSeparator.FindStringSubmatch(stripNoise(title)); m != nil {
			artist = strings.TrimSpace(m[1])
			title = strings.TrimSpace(m[2])
		}
	}

	base, version := cleanTitle(title)
	return SearchQuery{Title: base, Artist: artist}, version
}

// Applied to the query and to every candidate alike.
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

	// Strip stacked suffixes ("- Live; 2000 Remaster") only while they are all markers.
	for {
		m := dashSuffixPattern.FindStringSubmatch(title)
		if m == nil {
			break
		}

		var suffixKeys, suffixLabels []string
		markersOnly := true
		for part := range strings.SplitSeq(m[2], ";") {
			part = strings.TrimSpace(part)
			if key, ok := versionKey(part); ok {
				suffixKeys = append(suffixKeys, key)
				suffixLabels = append(suffixLabels, part)
				continue
			}
			if !remasterPattern.MatchString(part) {
				markersOnly = false
				break
			}
		}
		if !markersOnly {
			break
		}

		keys = append(keys, suffixKeys...)
		labels = append(labels, suffixLabels...)
		title = m[1]
	}

	title = trailingFeaturingPattern.ReplaceAllString(title, "")
	return strings.Join(strings.Fields(title), " "), newVersion(keys, labels)
}

func stripNoise(title string) string {
	return featuringPattern.ReplaceAllString(promoPattern.ReplaceAllString(title, ""), "")
}
