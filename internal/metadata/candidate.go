package metadata

import (
	"strings"
	"time"
)

// minDurationTolerance absorbs the silence and fades that differ between two
// uploads of the same recording.
const minDurationTolerance = 3 * time.Second

// source is what is known about the file being tagged.
type source struct {
	query    SearchQuery
	version  Version
	duration time.Duration // zero when the file's length could not be read
}

// match is a candidate that survived the constraints.
type match struct {
	info TrackInfo // Confidence holds its score
	base string    // the candidate's cleaned title
	// donor marks the original recording standing in for a variant no provider
	// carries: its descriptive metadata is used, its identity is not.
	donor       bool
	delta       time.Duration // distance from the file's length, -1 if unknown
	providerIdx int
}

// durationFits reports whether a candidate can be the same recording as the
// file, judging by length alone. The rule is asymmetric on purpose: a file
// shorter than the candidate is another cut (an edit, a sped-up version, a
// truncated upload), while a longer one is usually a music video whose intro
// and outro wrap the same song.
func durationFits(file, candidate time.Duration) bool {
	if file <= 0 || candidate <= 0 {
		return true // missing data never vetoes
	}
	if file < candidate-max(minDurationTolerance, candidate/10) {
		return false
	}
	// Over twice as long is a loop or a whole album, not the song.
	return file <= 2*candidate
}

// durationDelta is how far apart the two lengths are, -1 when either is unknown.
func durationDelta(file, candidate time.Duration) time.Duration {
	if file <= 0 || candidate <= 0 {
		return -1
	}
	if file > candidate {
		return file - candidate
	}
	return candidate - file
}

// better reports whether a should be preferred over b: higher score first, then
// the album closer to the file's own album tag, then the length closer to the
// file's. A full tie keeps b, the provider's own earlier ranking.
func better(a, b match, queryAlbum string) bool {
	if a.info.Confidence != b.info.Confidence {
		return a.info.Confidence > b.info.Confidence
	}
	if queryAlbum != "" && a.info.Album != "" && b.info.Album != "" {
		q := normalize(queryAlbum)
		simA := similarity(q, normalize(a.info.Album))
		simB := similarity(q, normalize(b.info.Album))
		if simA != simB {
			return simA > simB
		}
	}
	if a.delta < 0 {
		return false
	}
	return b.delta < 0 || a.delta < b.delta
}

// score computes a similarity score (0.0-1.0) between the query and a result.
func score(query SearchQuery, result TrackInfo) float64 {
	titleScore := similarity(normalize(query.Title), normalize(result.Title))
	artistScore := similarity(normalize(query.Artist), normalize(result.Artist))

	var s float64
	if query.Artist == "" {
		s = titleScore
	} else {
		// Weight: 60% title, 40% artist
		s = titleScore*0.6 + artistScore*0.4
	}

	// Boost results that match the existing album tag from yt-dlp
	if query.Album != "" && result.Album != "" {
		albumScore := similarity(normalize(query.Album), normalize(result.Album))
		if albumScore > 0.8 {
			s *= 1.1
		}
	}

	// Penalize compilation albums so original releases are preferred
	if strings.EqualFold(result.AlbumArtist, "Various Artists") {
		s *= 0.8
	}

	// Clamp to 1.0
	if s > 1.0 {
		s = 1.0
	}

	return s
}

// evaluate applies the constraints to every result and returns the best exact
// candidate — same version as the file, plausible length — and the best donor:
// the original recording, offered only when the file declares a variant. Donors
// skip the length check, since a variant is expected to differ from the
// original in length.
func (r *Resolver) evaluate(src source, results []TrackInfo) (exact, donor *match) {
	for _, res := range results {
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

// asVariant turns the original recording's metadata into metadata for the
// variant the file is: the title keeps the variant, and the fields that
// identify the original recording are dropped, so the variant is never
// mistaken for it or collides with it in the library.
func asVariant(info TrackInfo, base string, v Version) TrackInfo {
	info.Title = base + " (" + v.Label + ")"
	info.ISRC = ""
	info.TrackNumber = 0
	info.TotalTracks = 0
	info.DiscNumber = 0
	return info
}
