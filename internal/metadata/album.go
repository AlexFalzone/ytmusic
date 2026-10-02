package metadata

import (
	"context"
	"fmt"

	"go.senan.xyz/taglib"
)

const trackMatchThreshold = 0.6

func (r *Resolver) resolveGroup(ctx context.Context, album string, files []string, ar AlbumResolver) error {
	artist := ""
	if len(files) > 0 {
		if tags, err := r.tags.read(files[0]); err == nil {
			artist = FirstTag(tags, taglib.Artist)
		}
	}

	tl, found, err := ar.ResolveAlbum(ctx, album, artist)
	if err != nil {
		return fmt.Errorf("resolve album %q: %w", album, err)
	}
	if !found || len(tl.Tracks) == 0 {
		return nil
	}

	r.writeTrackPositions(files, tl, "album-first")
	return nil
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

	return r.writeTrackPositions(files, tl, "batch fingerprint")
}

func (r *Resolver) writeTrackPositions(files []string, tl Tracklist, phase string) []string {
	var written []string
	for _, path := range files {
		tags, err := r.tags.read(path)
		if err != nil {
			continue
		}
		title := FirstTag(tags, taglib.Title)
		if title == "" {
			continue
		}

		track, matchScore := matchTrackByTitle(title, tl.Tracks)
		if matchScore < trackMatchThreshold {
			r.logger.Debug("  %s: low match %.2f for %q, skipping", phase, matchScore, title)
			continue
		}

		r.logger.Debug("  %s: %q → track %d disc %d (score %.2f)", phase, title, track.TrackNumber, track.DiscNumber, matchScore)
		if err := r.tags.writePositional(path, track.TrackNumber, track.DiscNumber); err != nil {
			r.logger.Warn("  %s: failed to write positional tags for %q: %v", phase, path, err)
			continue
		}
		written = append(written, path)
	}
	return written
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

func filterResolved(files []string, resolved map[string]bool) []string {
	var out []string
	for _, f := range files {
		if !resolved[f] {
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

func matchTrackByTitle(fileTitle string, tracks []ReleaseTrack) (ReleaseTrack, float64) {
	best := tracks[0]
	bestScore := Similarity(fileTitle, tracks[0].Title)
	for _, t := range tracks[1:] {
		s := Similarity(fileTitle, t.Title)
		if s > bestScore {
			bestScore = s
			best = t
		}
	}
	return best, bestScore
}
