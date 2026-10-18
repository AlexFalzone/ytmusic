package metadata

import (
	"context"
	"errors"
)

// AcoustID links some fingerprints to the wrong recordings. "HYFR (Hell Ya Fuckin' Right)" against "Fucking" covers 4 of 5 words; "Animal Instinct" against "Animal", 1 of 2.
const fingerprintTitleCoverage = 0.75

func (r *Resolver) fingerprintMatch(ctx context.Context, path string, src source, rawTitle, rawArtist string) (TrackInfo, bool) {
	if r.fingerprinter == nil {
		return TrackInfo{}, false
	}
	info, found, err := r.fingerprintLookup(ctx, path, src, rawTitle, rawArtist)
	switch {
	case err == nil:
		return info, found
	case ctx.Err() != nil:
		// A cancel kills every fpcalc in flight: those errors say nothing about the files.
	case errors.Is(err, ErrFingerprintOff):
		r.fingerprintOff.Do(func() { r.logger.Warn("%v", err) })
	default:
		r.logger.Warn("  fingerprint lookup failed for %s: %v", path, err)
	}
	return TrackInfo{}, false
}

// The recordings the file confirms compete under the same version and length rules as a provider's results.
func (r *Resolver) fingerprintLookup(ctx context.Context, path string, src source, rawTitle, rawArtist string) (TrackInfo, bool, error) {
	recs, err := r.fingerprinter.Recordings(ctx, path)
	if err != nil || len(recs) == 0 {
		return TrackInfo{}, false, err
	}
	best, _ := r.evaluate(src, fingerprintCandidates(rawTitle, rawArtist, src.query.Album, recs))
	if best == nil {
		r.logger.Debug("  fingerprint: none of %d recordings fits the file", len(recs))
		return TrackInfo{}, false, nil
	}
	info, err := r.fingerprinter.Track(ctx, best.info.MBID, best.info.Album)
	if err != nil {
		return TrackInfo{}, false, err
	}
	info.Confidence = 1.0
	return info, true, nil
}

// A YouTube title holds the song's title among other words, and names the artist when a label uploaded it.
func fingerprintCandidates(rawTitle, rawArtist, album string, recs []Recording) []TrackInfo {
	var candidates []TrackInfo
	for _, rec := range recs {
		base, _ := cleanTitle(rec.Title)
		if coverage(stripNoise(rawTitle), base) < fingerprintTitleCoverage || !mentions(rawArtist+" "+unused(rawTitle, base), rec.Artist) {
			continue
		}
		home := homeAlbum(rec.Albums, album)
		candidates = append(candidates, TrackInfo{
			MBID:        rec.MBID,
			Title:       rec.Title,
			Artist:      rec.Artist,
			Duration:    rec.Duration,
			Album:       home.Title,
			AlbumArtist: home.Artist,
		})
	}
	return candidates
}

// The release group most like the file's album; then the first that is no compilation, soundtrack or the like.
func homeAlbum(albums []RecordingAlbum, fileAlbum string) RecordingAlbum {
	var home RecordingAlbum
	bestSim, found := -1.0, false
	for _, a := range albums {
		sim := 0.0
		if fileAlbum != "" {
			sim = Similarity(fileAlbum, a.Title)
		}
		if !found || sim > bestSim || (sim == bestSim && home.Secondary && !a.Secondary) {
			home, bestSim, found = a, sim, true
		}
	}
	return home
}
