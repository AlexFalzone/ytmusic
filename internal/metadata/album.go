package metadata

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	"go.senan.xyz/taglib"
)

const trackMatchThreshold = 0.6

func (r *Resolver) resolveGroup(ctx context.Context, album string, files []string, ar AlbumResolver) ([]string, error) {
	artist := ""
	if len(files) > 0 {
		if tags, err := r.tags.read(files[0]); err == nil {
			artist = FirstTag(tags, taglib.Artist)
		}
	}

	// The whole name first: the comma may be part of it.
	for _, name := range slices.Compact([]string{artist, primaryArtist(artist)}) {
		tl, found, err := ar.ResolveAlbum(ctx, album, name)
		if err != nil {
			return nil, fmt.Errorf("resolve album %q: %w", album, err)
		}
		if found && len(tl.Tracks) > 0 {
			return r.placeOnRelease(ctx, files, tl, "album-first"), nil
		}
	}
	return nil, nil
}

func (r *Resolver) resolveGroupByFingerprint(ctx context.Context, files []string) []string {
	matches, err := r.batchFingerprinter.BatchLookupByFiles(ctx, files)
	if err != nil {
		r.logger.Error("batch fingerprint: %v", err)
	}
	if len(files) == 0 {
		return nil
	}
	coverage := float64(len(matches)) / float64(len(files))
	if coverage < 0.5 {
		r.logger.Debug("  batch fingerprint: coverage %.0f%% below 50%%, skipping group", coverage*100)
		return nil
	}

	mbids := make([]string, 0, len(matches))
	for _, m := range matches {
		mbids = append(mbids, m.MBID)
	}

	dominantID, found := r.findDominantRelease(ctx, mbids)
	if !found {
		r.logger.Debug("  batch fingerprint: no dominant release found")
		return nil
	}

	tl, err := r.releaseResolver.LookupTracklist(ctx, dominantID)
	if err != nil || len(tl.Tracks) == 0 {
		r.logger.Debug("  batch fingerprint: tracklist lookup failed or empty")
		return nil
	}

	r.logger.Debug("  batch fingerprint: dominant release %q (%s)", tl.Title, dominantID)

	return r.placeOnRelease(ctx, files, tl, "batch fingerprint")
}

// Everything album-level comes from tl, or number and album can end up from two releases.
func (r *Resolver) placeOnRelease(ctx context.Context, files []string, tl Tracklist, phase string) []string {
	var placed []string
	for _, p := range r.assignTracks(files, tl.Tracks, phase) {
		r.logger.Debug("  %s: %s → track %d disc %d (score %.2f)", phase, p.path, p.track.TrackNumber, p.track.DiscNumber, p.score)
		if err := r.tags.write(p.path, releaseTags(tl, p.track)); err != nil {
			r.logger.Warn("  %s: writing tags to %s: %v", phase, p.path, err)
			continue
		}
		placed = append(placed, p.path)
	}
	if len(placed) == 0 {
		return nil
	}

	cover := r.albumCover(ctx, tl, placed)
	for _, path := range placed {
		if err := WriteArtwork(path, cover); err != nil {
			r.logger.Warn("  %s: %v", phase, err)
		}
	}
	return placed
}

func releaseTags(tl Tracklist, track ReleaseTrack) map[string][]string {
	return tagMap(TrackInfo{
		Album: tl.Title, AlbumArtist: tl.Artist, ReleaseDate: tl.Date,
		TrackNumber: track.TrackNumber, DiscNumber: track.DiscNumber,
	})
}

type placement struct {
	path  string
	track ReleaseTrack
	score float64
}

// One file per track, best scores first: a remix at 0.67 on its original must not take the number the original holds.
func (r *Resolver) assignTracks(files []string, tracks []ReleaseTrack, phase string) []placement {
	var pairs []placement
	for _, path := range files {
		tags, err := r.tags.read(path)
		if err != nil {
			continue
		}
		title := FirstTag(tags, taglib.Title)
		if title == "" {
			continue
		}
		_, version := cleanTitle(title)
		for _, track := range tracks {
			_, trackVersion := cleanTitle(track.Title)
			if s := Similarity(title, track.Title); s >= trackMatchThreshold && version.Key == trackVersion.Key {
				pairs = append(pairs, placement{path: path, track: track, score: s})
			}
		}
	}
	slices.SortStableFunc(pairs, func(a, b placement) int { return cmp.Compare(b.score, a.score) })

	type position struct{ disc, number int }
	taken := make(map[position]bool)
	assigned := make(map[string]bool)
	var placements []placement
	for _, p := range pairs {
		at := position{p.track.DiscNumber, p.track.TrackNumber}
		if !taken[at] && !assigned[p.path] {
			taken[at], assigned[p.path] = true, true
			placements = append(placements, p)
		}
	}
	for _, path := range files {
		if !assigned[path] {
			r.logger.Debug("  %s: no track for %s", phase, path)
		}
	}
	return placements
}

func (r *Resolver) findDominantRelease(ctx context.Context, mbids []string) (string, bool) {
	if len(mbids) == 0 {
		return "", false
	}

	counts := make(map[string]int)
	for _, mbid := range mbids {
		ids, err := r.releaseResolver.ReleaseIDsForRecording(ctx, mbid)
		if err != nil {
			continue
		}
		for _, id := range ids {
			counts[id]++
		}
	}

	quorum := float64(len(mbids)) * 0.5

	bestID := ""
	bestCount := 0
	for id, count := range counts {
		// Ties go to the lowest ID, so map order cannot change the result.
		if count > bestCount || (count == bestCount && (bestID == "" || id < bestID)) {
			bestCount = count
			bestID = id
		}
	}

	if float64(bestCount) >= quorum {
		return bestID, true
	}
	return "", false
}

func filterResolved(files []string, resolved map[string]scope) []string {
	var out []string
	for _, f := range files {
		if _, ok := resolved[f]; !ok {
			out = append(out, f)
		}
	}
	return out
}

func (r *Resolver) groupByAlbum(files []string) map[string][]string {
	groups := make(map[string][]string)
	for _, path := range files {
		tags, err := r.tags.read(path)
		if err != nil {
			continue
		}
		album := FirstTag(tags, taglib.Album)
		groups[album] = append(groups[album], path)
	}
	return groups
}
