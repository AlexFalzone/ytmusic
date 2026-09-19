package metadata

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// fuzzyTokenThreshold is the Jaro-Winkler similarity from which two tokens
// count as the same word. It catches plurals and one-letter typos
// ("light"/"lights" 0.967) while keeping different words apart
// ("walking"/"talking" 0.905, "love"/"live" 0.850).
const fuzzyTokenThreshold = 0.92

// fuzzyTokenMinLen keeps short words exact: one letter changes their meaning
// ("me"/"we"), and they are too short for Jaro-Winkler to tell apart.
const fuzzyTokenMinLen = 4

// normalize prepares a string for comparison: lowercase, accents stripped,
// "&" spelled "and", punctuation dropped, whitespace collapsed. Its output is
// only ever compared, never written to a tag.
func normalize(s string) string {
	var b strings.Builder
	for _, r := range norm.NFD.String(strings.ToLower(s)) {
		switch {
		case unicode.Is(unicode.Mn, r):
			// An accent split off by NFD: dropping it makes "é" compare as "e".
		case r == '&':
			b.WriteString(" and ")
		case unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsSpace(r):
			b.WriteRune(r)
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// similarity returns how alike two normalized strings are (0.0-1.0): the share
// of tokens they have in common, where near-identical tokens count as shared.
// Comparing without spaces first handles "theweeknd" vs "the weeknd".
func similarity(a, b string) float64 {
	if a == "" && b == "" {
		return 1.0
	}
	if a == "" || b == "" {
		return 0.0
	}
	if strings.ReplaceAll(a, " ", "") == strings.ReplaceAll(b, " ", "") {
		return 1.0
	}

	tokensA, tokensB := strings.Fields(a), strings.Fields(b)
	if len(tokensA) == 0 || len(tokensB) == 0 {
		return 0.0
	}

	matches := 0
	for _, ta := range tokensA {
		for _, tb := range tokensB {
			if tokensMatch(ta, tb) {
				matches++
				break
			}
		}
	}
	return float64(matches) / float64(max(len(tokensA), len(tokensB)))
}

// tokensMatch reports whether two tokens are the same word, allowing a plural
// or a typo on words long enough for that to be safe.
func tokensMatch(a, b string) bool {
	if a == b {
		return true
	}
	if utf8.RuneCountInString(a) < fuzzyTokenMinLen || utf8.RuneCountInString(b) < fuzzyTokenMinLen {
		return false
	}
	return jaroWinkler(a, b) >= fuzzyTokenThreshold
}

// jaroWinkler returns the Jaro-Winkler similarity of a and b (0.0-1.0).
// It is applied to single tokens only: on whole strings it stays around 0.6
// even for titles with nothing in common.
func jaroWinkler(a, b string) float64 {
	ra, rb := []rune(a), []rune(b)
	j := jaro(ra, rb)

	prefix := 0
	for i := 0; i < min(4, len(ra), len(rb)) && ra[i] == rb[i]; i++ {
		prefix++
	}
	return j + float64(prefix)*0.1*(1-j)
}

func jaro(a, b []rune) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 1.0
	}
	if len(a) == 0 || len(b) == 0 {
		return 0.0
	}

	window := max(max(len(a), len(b))/2-1, 0)
	matchedA := make([]bool, len(a))
	matchedB := make([]bool, len(b))

	matches := 0
	for i := range a {
		for j := max(0, i-window); j < min(i+window+1, len(b)); j++ {
			if !matchedB[j] && a[i] == b[j] {
				matchedA[i], matchedB[j] = true, true
				matches++
				break
			}
		}
	}
	if matches == 0 {
		return 0.0
	}

	transpositions := 0
	k := 0
	for i := range a {
		if !matchedA[i] {
			continue
		}
		for !matchedB[k] {
			k++
		}
		if a[i] != b[k] {
			transpositions++
		}
		k++
	}

	m := float64(matches)
	return (m/float64(len(a)) + m/float64(len(b)) + (m-float64(transpositions)/2)/m) / 3
}
