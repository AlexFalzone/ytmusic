package metadata

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// "light"/"lights" 0.967 passes; "walking"/"talking" 0.905 does not.
const fuzzyTokenThreshold = 0.92

// Shorter words differ by a letter and pass the threshold: "lock"/"clock" 0.933.
const fuzzyTokenMinLen = 7

// Otherwise "i" and "is" would be the same token.
const pluralMinLen = 3

func normalize(s string) string {
	var b strings.Builder
	for _, r := range norm.NFD.String(strings.ToLower(s)) {
		switch {
		case unicode.Is(unicode.Mn, r):
			// An accent split off by NFD.
		case r == '&':
			b.WriteString(" and ")
		case unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsSpace(r):
			b.WriteRune(r)
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func Similarity(a, b string) float64 {
	return similarity(normalize(a), normalize(b))
}

func similarity(a, b string) float64 {
	if a == "" && b == "" {
		return 1.0
	}
	if a == "" || b == "" {
		return 0.0
	}
	if strings.ReplaceAll(a, " ", "") == strings.ReplaceAll(b, " ", "") { // "theweeknd"
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

func tokensMatch(a, b string) bool {
	if a == b {
		return true
	}
	// Apart from the edit distance, which cannot tell "light"/"lights" from "lock"/"clock".
	if isPlural(a, b) || isPlural(b, a) {
		return true
	}
	if min(utf8.RuneCountInString(a), utf8.RuneCountInString(b)) < fuzzyTokenMinLen {
		return false
	}
	return jaroWinkler(a, b) >= fuzzyTokenThreshold
}

func isPlural(plural, singular string) bool {
	if utf8.RuneCountInString(singular) < pluralMinLen {
		return false
	}
	return plural == singular+"s" || plural == singular+"es"
}

// Tokens only: on whole strings it stays around 0.6 even for unrelated titles.
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
