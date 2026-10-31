package metadata

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.senan.xyz/taglib"
)

// trackOnly: the album phase owns the album-level fields and the cover, so only the track's own fields are written.
func (r *Resolver) resolveFile(ctx context.Context, path string, trackOnly bool) error {
	existing, err := r.tags.read(path)
	if err != nil {
		return fmt.Errorf("failed to read existing tags: %w", err)
	}

	rawTitle := FirstTag(existing, taglib.Title)
	rawArtist := FirstTag(existing, taglib.Artist)
	rawAlbum := FirstTag(existing, taglib.Album)

	if rawTitle == "" {
		r.logger.Debug("  Skipping: no title metadata")
		return nil
	}

	query, version := NormalizeQuery(rawTitle, rawArtist)
	query.Album = strings.TrimSpace(rawAlbum)
	if query.Title == "" {
		return nil
	}

	src := source{query: query, version: version, duration: r.fileDuration(path)}
	r.logger.Debug("  Normalized: title=%q artist=%q album=%q version=%q length=%s",
		query.Title, query.Artist, query.Album, version.Key, src.duration.Round(time.Second))

	if info, found := r.fingerprintMatch(ctx, path, src, rawTitle, rawArtist); found {
		r.logger.Debug("  Fingerprint match: %q by %q", info.Title, info.Artist)
		return r.finish(ctx, path, existing, src, match{info: info, providerIdx: -1}, trackOnly)
	}

	m, ok := r.findPrimaryMatch(ctx, src)
	if !ok {
		r.logger.Debug("  No candidate above threshold %.2f, keeping original tags", r.threshold)
		if trackOnly {
			return nil
		}
		if artist := albumArtistFallback(existing, ""); artist != "" {
			if err := r.tags.write(path, map[string][]string{taglib.AlbumArtist: {artist}}); err != nil {
				r.logger.Warn("  writing album artist to %s: %v", path, err)
			}
		}
		return nil
	}

	return r.finish(ctx, path, existing, src, m, trackOnly)
}

func (r *Resolver) finish(ctx context.Context, path string, existing map[string][]string, src source, m match, trackOnly bool) error {
	if trackOnly {
		hasISRC := FirstTag(existing, taglib.ISRC) != ""
		info := r.fillGaps(ctx, src, m, nil, func(t TrackInfo) bool {
			return t.Genre == "" || (t.ISRC == "" && !hasISRC)
		})
		if m.donor {
			info = asVariant(info, m.base, src.version)
		}
		if err := r.tags.write(path, trackTagMap(info, existing)); err != nil {
			return fmt.Errorf("failed to write tags to %s: %w", path, err)
		}
		return nil
	}

	info, art := r.complete(ctx, src, m)
	if m.donor {
		info = asVariant(info, m.base, src.version)
	}
	return r.writeResolved(path, existing, info, art)
}

// Zero means unknown, which disables the duration check instead of failing the file.
func (r *Resolver) fileDuration(path string) time.Duration {
	props, err := taglib.ReadProperties(path)
	if err != nil {
		r.logger.Debug("  could not read length: %v", err)
		return 0
	}
	return props.Length
}

func (r *Resolver) writeResolved(path string, existing map[string][]string, info TrackInfo, art []byte) error {
	info = mergeWithExisting(existing, info)
	if info.AlbumArtist == "" {
		info.AlbumArtist = albumArtistFallback(existing, info.Artist)
	}
	if err := r.tags.write(path, tagMap(info)); err != nil {
		return fmt.Errorf("failed to write tags to %s: %w", path, err)
	}
	if err := WriteArtwork(path, art); err != nil {
		r.logger.Warn("  Failed to embed artwork: %v", err)
	}
	return nil
}

func (r *Resolver) search(ctx context.Context, p Provider, query SearchQuery) ([]TrackInfo, bool) {
	results, err := p.Search(ctx, query)
	if err != nil {
		r.logger.Debug("  provider %s failed: %v", p.Name(), err)
		if ctx.Err() == nil {
			r.failures.add(p.Name(), err)
		}
		return nil, false
	}
	return results, true
}

// An exact match ends the search; a donor waits for every provider to offer the variant itself.
func (r *Resolver) findPrimaryMatch(ctx context.Context, src source) (match, bool) {
	var donor *match
	for i, p := range r.providers {
		results, ok := r.search(ctx, p, src.query)
		if !ok {
			continue
		}
		if len(results) == 0 {
			r.logger.Debug("  No results from %s", p.Name())
			continue
		}

		exact, d := r.evaluate(src, results)
		if exact != nil {
			r.logger.Debug("  %s: best %q by %q (confidence: %.2f)", p.Name(), exact.info.Title, exact.info.Artist, exact.info.Confidence)
			if exact.info.Confidence >= r.threshold {
				exact.providerIdx = i
				return *exact, true
			}
		}
		if donor == nil && d != nil && d.info.Confidence >= r.threshold {
			d.providerIdx = i
			donor = d
		}
	}

	if donor != nil {
		r.logger.Debug("  no provider has the %q version, borrowing metadata from %q", src.version.Label, donor.info.Title)
		return *donor, true
	}
	return match{}, false
}

// Artwork URLs in failedArtwork count as missing, wherever they come from. missing says which gaps are worth another provider.
func (r *Resolver) fillGaps(ctx context.Context, src source, primary match, failedArtwork map[string]bool, missing func(TrackInfo) bool) TrackInfo {
	base := primary.info
	if failedArtwork[base.ArtworkURL] {
		base.ArtworkURL = ""
	}
	if !missing(base) {
		return base
	}

	for _, p := range r.providers[primary.providerIdx+1:] {
		results, ok := r.search(ctx, p, src.query)
		if !ok || len(results) == 0 {
			continue
		}

		exact, donor := r.evaluate(src, results)
		filler := exact
		if primary.donor {
			filler = donor
		}
		if filler == nil || filler.info.Confidence < r.threshold {
			continue
		}

		r.logger.Debug("  gap fill from %s: %q by %q", p.Name(), filler.info.Title, filler.info.Artist)
		fill := filler.info
		if failedArtwork[fill.ArtworkURL] {
			fill.ArtworkURL = ""
		}
		base = mergeTrackInfo(base, fill)

		if !missing(base) {
			break
		}
	}

	return base
}

func hasMissingFields(t TrackInfo) bool {
	return t.Genre == "" ||
		t.TrackNumber == 0 ||
		t.DiscNumber == 0 ||
		t.Year == 0 ||
		t.ISRC == "" ||
		t.ArtworkURL == ""
}

// Title, Artist, Album and AlbumArtist come from the primary match only.
func mergeTrackInfo(base, filler TrackInfo) TrackInfo {
	if base.Genre == "" && filler.Genre != "" {
		base.Genre = filler.Genre
	}
	if base.TrackNumber == 0 && filler.TrackNumber != 0 {
		base.TrackNumber = filler.TrackNumber
	}
	if base.TotalTracks == 0 && filler.TotalTracks != 0 {
		base.TotalTracks = filler.TotalTracks
	}
	if base.DiscNumber == 0 && filler.DiscNumber != 0 {
		base.DiscNumber = filler.DiscNumber
	}
	if base.Year == 0 && filler.Year != 0 {
		base.Year = filler.Year
	}
	if base.ReleaseDate == "" && filler.ReleaseDate != "" {
		base.ReleaseDate = filler.ReleaseDate
	}
	if base.ISRC == "" && filler.ISRC != "" {
		base.ISRC = filler.ISRC
	}
	if base.ArtworkURL == "" && filler.ArtworkURL != "" {
		base.ArtworkURL = filler.ArtworkURL
	}
	return base
}
