package metadata

import (
	"regexp"
	"slices"
	"strings"
)

// The zero value is the original recording.
type Version struct {
	Key   string // "acoustic+live", whatever the wording or order
	Label string // as the title spelled it: "Skrillex Remix"
}

func (v Version) IsOriginal() bool { return v.Key == "" }

// Whole delimited segments only: "Live Forever" is a title, not a live version.
var versionMarkers = []struct {
	key     string
	pattern *regexp.Regexp
}{
	{"live", regexp.MustCompile(`(?i)^live(\s+(version|session|recording)|\s+(at|in|from|on)\s+.+)?$`)},
	{"remix", regexp.MustCompile(`(?i)^((.+\s+)?(remix|rmx)|remix(ed)?\s+by\s+.+)$`)},
	{"acoustic", regexp.MustCompile(`(?i)^acoustic(\s+version)?$`)},
	{"instrumental", regexp.MustCompile(`(?i)^instrumental(\s+version)?$`)},
	{"sped up", regexp.MustCompile(`(?i)^(sped\s+up|nightcore)(\s*(\+|&|and)\s*reverb)?(\s+version)?$`)},
	{"slowed", regexp.MustCompile(`(?i)^slowed(\s*(\+|&|and)\s*reverb(ed)?)?(\s+version)?$`)},
	{"edit", regexp.MustCompile(`(?i)^(radio|single)\s+edit$`)},
	{"extended", regexp.MustCompile(`(?i)^extended(\s+(mix|version))?$`)},
	{"demo", regexp.MustCompile(`(?i)^demo(\s+version)?$`)},
	{"karaoke", regexp.MustCompile(`(?i)^karaoke(\s+version)?$`)},
	{"acapella", regexp.MustCompile(`(?i)^a\s*cap+el+a(\s+version)?$`)},
}

// What a live label says beyond "live".
var liveWords = regexp.MustCompile(`(?i)^live(\s+(version|session|recording))?(\s+(at|in|from|on))?\s*`)

// "the Forum, 1976" for "Live at the Forum, 1976"; "" when the label names no concert.
func (v Version) concert() string {
	if v.Key != "live" {
		return ""
	}
	return strings.TrimSpace(liveWords.ReplaceAllString(v.Label, ""))
}

var numbers = regexp.MustCompile(`\d+`)

// Each side may say less than the other, "1976" against "10/20/1976", but nothing against it: 7/27 is not 7/28.
func sameConcert(a, b string) bool {
	numsA, numsB := numbers.FindAllString(a, -1), numbers.FindAllString(b, -1)
	if len(numsA) > len(numsB) {
		numsA, numsB = numsB, numsA
	}
	for _, n := range numsA {
		if !slices.ContainsFunc(numsB, func(other string) bool { return sameNumber(n, other) }) {
			return false
		}
	}

	wordsA, wordsB := placeWords(a), placeWords(b)
	if len(wordsA) > len(wordsB) {
		wordsA, wordsB = wordsB, wordsA
	}
	return len(wordsA) == 0 || 2*countFound(wordsA, wordsB) >= len(wordsA)
}

// '86 and 1986.
func sameNumber(a, b string) bool {
	return a == b || len(a) == 2 && len(b) == 4 && strings.HasSuffix(b, a) || len(b) == 2 && len(a) == 4 && strings.HasSuffix(a, b)
}

func placeWords(concert string) []string {
	var words []string
	for word := range strings.FieldsSeq(normalize(numbers.ReplaceAllString(concert, " "))) {
		if !connectors[word] {
			words = append(words, word)
		}
	}
	return words
}

func versionKey(segment string) (string, bool) {
	s := strings.TrimSpace(segment)
	for _, m := range versionMarkers {
		if m.pattern.MatchString(s) {
			return m.key, true
		}
	}
	return "", false
}

func newVersion(keys, labels []string) Version {
	if len(keys) == 0 {
		return Version{}
	}
	keys = slices.Clone(keys)
	slices.Sort(keys)
	return Version{
		Key:   strings.Join(slices.Compact(keys), "+"),
		Label: strings.Join(labels, ", "),
	}
}
