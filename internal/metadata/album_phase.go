package metadata

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"go.senan.xyz/taglib"
)

// Measured on real albums: the right edition scores 0.95-1, another edition with the same track count 0.77.
const editionMatchRatio = 0.9

// "(Deluxe)", "[New Edition]": MusicBrainz keeps the edition out of the title.
var trailingSegment = regexp.MustCompile(`\s*[\(\[][^\(\)\[\]]*[\)\]]$`)

// Album-level fields are decided once here, for every file, whether or not a provider later recognises the track.
func (r *Resolver) resolveAlbum(ctx context.Context, album *Album) {
	paths := albumPaths(album)
	var albums, artists, wholeArtists, dates []string
	for _, path := range paths {
		tags, err := r.tags.read(path)
		if err != nil {
			r.logger.Warn("album: reading tags of %s: %v", path, err)
			continue
		}
		albums = append(albums, FirstTag(tags, taglib.Album))
		artists = append(artists, primaryArtist(FirstTag(tags, taglib.Artist)))
		wholeArtists = append(wholeArtists, FirstTag(tags, taglib.Artist))
		dates = append(dates, FirstTag(tags, taglib.Date))
	}

	artist := albumArtist(artists)
	described, _ := mostCommon(albums)
	release, found := r.findEdition(ctx, album, artist, described)

	date, _ := mostCommon(dates)
	if found {
		r.logger.Info("album %q: MusicBrainz release %s (%s, %s)", album.Title, release.ID, release.Title, release.Date)
		if release.Date != "" {
			date = release.Date
		}
		artist = creditedArtist(artist, release.Artist, wholeArtists)
	} else {
		r.logger.Info("album %q: no MusicBrainz release with these %d tracks, keeping YouTube Music's data", album.Title, len(album.Tracks))
	}

	for _, path := range paths {
		pos := album.Files[path]
		tags := map[string][]string{
			taglib.Album:       {album.Title},
			taglib.TrackNumber: {fmt.Sprintf("%d/%d", pos, len(album.Tracks))},
		}
		if artist != "" {
			tags[taglib.AlbumArtist] = []string{artist}
		}
		if date != "" {
			tags[taglib.Date] = []string{date}
		}
		if found {
			track := release.Tracks[pos-1]
			if track.ISRC != "" {
				tags[taglib.ISRC] = []string{track.ISRC}
			}
			if existing, err := r.tags.read(path); err == nil && sameTrackTitle(FirstTag(existing, taglib.Title), track.Title) {
				tags[taglib.Title] = []string{track.Title}
			}
		}
		if err := r.tags.write(path, tags); err != nil {
			r.logger.Warn("album: writing tags to %s: %v", path, err)
		}
	}

	cover := r.albumCover(ctx, release, paths)
	for _, path := range paths {
		if err := WriteArtwork(path, cover); err != nil {
			r.logger.Warn("album: %v", err)
		}
	}
}

// In album order, so ties in the votes below go to the earliest track. A position outside the album leaves the file out.
func albumPaths(album *Album) []string {
	paths := make([]string, 0, len(album.Files))
	for path, pos := range album.Files {
		if pos >= 1 && pos <= len(album.Tracks) {
			paths = append(paths, path)
		}
	}
	slices.SortFunc(paths, func(a, b string) int { return album.Files[a] - album.Files[b] })
	return paths
}

func (r *Resolver) findEdition(ctx context.Context, album *Album, artist, described string) (Tracklist, bool) {
	if r.releaseFinder == nil {
		return Tracklist{}, false
	}
	for _, title := range editionTitles(album.Title, described) {
		candidates, err := r.releaseFinder.FindReleases(ctx, title, artist, len(album.Tracks))
		// Shown by default: MusicBrainz often answers 503, which would otherwise read as "no such release".
		if err != nil {
			r.logger.Warn("album: MusicBrainz search for %q: %v", title, err)
		}
		if tl, ok := matchRelease(album.Tracks, candidates); ok {
			return tl, true
		}
	}
	return Tracklist{}, false
}

// The title as presented, the album name in the tracks' descriptions, then the title without its edition.
func editionTitles(title, described string) []string {
	base := title
	for trailingSegment.MatchString(base) {
		base = trailingSegment.ReplaceAllString(base, "")
	}

	var titles []string
	for _, t := range []string{title, described, base} {
		if t != "" && !slices.Contains(titles, t) {
			titles = append(titles, t)
		}
	}
	return titles
}

// The release's front cover, else YouTube's thumbnail cropped square: one image for every track.
func (r *Resolver) albumCover(ctx context.Context, release Tracklist, paths []string) []byte {
	if release.ArtworkURL != "" {
		art, err := r.downloadArtwork(ctx, release.ArtworkURL)
		if err == nil {
			return art
		}
		r.logger.Debug("  album: dropping cover %s: %v", release.ArtworkURL, err)
	}

	for _, path := range paths {
		thumb, err := taglib.ReadImage(path)
		if err != nil || len(thumb) == 0 {
			continue
		}
		square, err := squareCover(thumb)
		if err != nil {
			r.logger.Debug("  album: keeping the thumbnail uncropped: %v", err)
			return thumb
		}
		return square
	}
	return nil
}

// The leading artist when it has more than half the tracks.
func albumArtist(artists []string) string {
	artist, n := mostCommon(artists)
	if artist == "" || n*2 > len(artists) {
		return artist
	}
	return "Various Artists"
}

// yt-dlp joins artists with ", ", so "Tyler, The Creator" reads as two: a release that credits the name whole settles it.
func creditedArtist(artist, credited string, wholeArtists []string) string {
	if primaryArtist(credited) != artist {
		return artist
	}
	for _, whole := range wholeArtists {
		if whole == credited || strings.HasPrefix(whole, credited+",") {
			return credited
		}
	}
	return artist
}

// The most frequent non-empty value; on a tie, the one that reached the count first.
func mostCommon(values []string) (string, int) {
	counts := make(map[string]int)
	best := ""
	for _, v := range values {
		if v == "" {
			continue
		}
		counts[v]++
		if counts[v] > counts[best] {
			best = v
		}
	}
	return best, counts[best]
}

// The release that holds the album as presented: same track count, titles in the same order.
func matchRelease(tracks []string, candidates []Tracklist) (Tracklist, bool) {
	var best Tracklist
	bestRatio := 0.0
	for _, tl := range candidates {
		ratio, ok := editionRatio(tracks, tl)
		if !ok || ratio < editionMatchRatio {
			continue
		}
		if bestRatio == 0 || ratio > bestRatio || (ratio == bestRatio && preferRelease(tl, best)) {
			best, bestRatio = tl, ratio
		}
	}
	return best, bestRatio > 0
}

// The share of known titles that match the release's title at the same position.
func editionRatio(tracks []string, tl Tracklist) (float64, bool) {
	if len(tl.Tracks) != len(tracks) {
		return 0, false
	}
	known, matched := 0, 0
	for i, title := range tracks {
		if title == "" {
			continue
		}
		known++
		if sameTrackTitle(title, tl.Tracks[i].Title) {
			matched++
		}
	}
	if known == 0 {
		return 0, false
	}
	return float64(matched) / float64(known), true
}

// YouTube Music cuts titles at about 100 characters, sometimes inside a parenthesis cleanTitle then cannot strip.
const truncatedTitle = 90

func sameTrackTitle(title, release string) bool {
	a, _ := cleanTitle(title)
	b, _ := cleanTitle(release)
	if Similarity(a, b) >= trackMatchThreshold {
		return true
	}
	return utf8.RuneCountInString(title) >= truncatedTitle && strings.HasPrefix(normalize(release), normalize(title))
}

// Not a clean edit (its ISRCs differ from the explicit album YouTube Music shows), Official, earliest year, fullest date, ID.
func preferRelease(a, b Tracklist) bool {
	if isClean(a) != isClean(b) {
		return !isClean(a)
	}
	if (a.Status == "Official") != (b.Status == "Official") {
		return a.Status == "Official"
	}
	if a.Date != b.Date {
		if a.Date == "" || b.Date == "" {
			return a.Date != ""
		}
		if ya, yb := ParseYear(a.Date), ParseYear(b.Date); ya != yb {
			return ya < yb
		}
		if len(a.Date) != len(b.Date) {
			return len(a.Date) > len(b.Date)
		}
		return a.Date < b.Date
	}
	return a.ID < b.ID
}

func isClean(tl Tracklist) bool {
	return strings.Contains(strings.ToLower(tl.Disambiguation), "clean")
}
