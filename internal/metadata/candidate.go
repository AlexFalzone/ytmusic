package metadata

import (
	"strings"
	"time"
)

// Silence and fades differ between two uploads of the same recording.
const minDurationTolerance = 3 * time.Second

type source struct {
	query    SearchQuery
	version  Version
	duration time.Duration // zero: unknown
}

type match struct {
	info TrackInfo
	base string // cleaned title
	// The original recording standing in for a variant no provider carries.
	donor       bool
	delta       time.Duration // -1: unknown
	providerIdx int
}

// Asymmetric: a shorter file is another cut, a longer one usually a music video around the same song.
func durationFits(file, candidate time.Duration) bool {
	if file <= 0 || candidate <= 0 {
		return true
	}
	if file < candidate-max(minDurationTolerance, candidate/10) {
		return false
	}
	// Over twice as long is a loop or a whole album, not the song.
	return file <= 2*candidate
}

func durationDelta(file, candidate time.Duration) time.Duration {
	if file <= 0 || candidate <= 0 {
		return -1
	}
	if file > candidate {
		return file - candidate
	}
	return candidate - file
}

// A full tie keeps b, the provider's own earlier ranking.
func better(a, b match, queryAlbum string) bool {
	if a.info.Confidence != b.info.Confidence {
		return a.info.Confidence > b.info.Confidence
	}
	if queryAlbum != "" && a.info.Album != "" && b.info.Album != "" {
		simA := Similarity(queryAlbum, a.info.Album)
		simB := Similarity(queryAlbum, b.info.Album)
		if simA != simB {
			return simA > simB
		}
	}
	if a.delta < 0 {
		return false
	}
	return b.delta < 0 || a.delta < b.delta
}

func score(query SearchQuery, result TrackInfo) float64 {
	titleScore := Similarity(query.Title, result.Title)
	artistScore := Similarity(query.Artist, result.Artist)

	var s float64
	if query.Artist == "" {
		s = titleScore
	} else {
		s = titleScore*0.6 + artistScore*0.4
	}

	if query.Album != "" && result.Album != "" {
		albumScore := Similarity(query.Album, result.Album)
		if albumScore > 0.8 {
			s *= 1.1
		}
	}

	if strings.EqualFold(result.AlbumArtist, "Various Artists") {
		s *= 0.8
	}

	return min(s, 1.0)
}

// Donors skip the length check: a variant is expected to differ in length.
func (r *Resolver) evaluate(src source, results []TrackInfo) (exact, donor *match) {
	for _, res := range results {
		// Nothing to check it against: a truncated clip once took such a recording's ISRC.
		if res.Duration == 0 && res.Album == "" {
			r.logger.Debug("  skip %q: neither a length nor an album to check it against", res.Title)
			continue
		}

		base, version := cleanTitle(res.Title)

		isDonor := false
		switch {
		case version.Key == src.version.Key:
			if !durationFits(src.duration, res.Duration) {
				r.logger.Debug("  skip %q: %s long, file is %s", res.Title, res.Duration.Round(time.Second), src.duration.Round(time.Second))
				continue
			}
		case !src.version.IsOriginal() && version.IsOriginal():
			isDonor = true
		default:
			r.logger.Debug("  skip %q: version %q, file declares %q", res.Title, version.Key, src.version.Key)
			continue
		}

		cmp := res
		cmp.Title = base
		res.Confidence = score(src.query, cmp)

		m := match{info: res, base: base, donor: isDonor, delta: durationDelta(src.duration, res.Duration)}
		if isDonor {
			if donor == nil || better(m, *donor, src.query.Album) {
				donor = &m
			}
		} else if exact == nil || better(m, *exact, src.query.Album) {
			exact = &m
		}
	}
	return exact, donor
}

// Drops what identifies the original, so the variant never collides with it in the library.
func asVariant(info TrackInfo, base string, v Version) TrackInfo {
	info.Title = base + " (" + v.Label + ")"
	info.ISRC = ""
	info.TrackNumber = 0
	info.TotalTracks = 0
	info.DiscNumber = 0
	return info
}
