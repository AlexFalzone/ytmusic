package metadata

import (
	"regexp"
	"slices"
	"strings"
)

// Version is the variant a title declares ("Live", "Sped Up"); the zero value
// is the original recording.
type Version struct {
	// Key identifies the variant whatever the wording or order: "live",
	// "acoustic+live". Empty for the original.
	Key string
	// Label is the variant as the title spelled it ("Skrillex Remix"), kept
	// for display when a variant borrows the original's metadata.
	Label string
}

// IsOriginal reports whether the title declares no variant.
func (v Version) IsOriginal() bool { return v.Key == "" }

// versionMarkers recognise a variant only when a whole delimited segment
// matches, which is what keeps "Song (Live at Wembley)" apart from the titles
// "Live Forever" and "Remix to Ignition".
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

// versionKey returns the variant a delimited segment declares, if any.
func versionKey(segment string) (string, bool) {
	s := strings.TrimSpace(segment)
	for _, m := range versionMarkers {
		if m.pattern.MatchString(s) {
			return m.key, true
		}
	}
	return "", false
}

// newVersion combines the markers found in one title.
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
