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
	{"remix", regexp.MustCompile(`(?i)^((.+\s+)?remix|remix(ed)?\s+by\s+.+)$`)},
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
