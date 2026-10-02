package metadata

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.senan.xyz/taglib"
)

func (r *Resolver) resolveFile(ctx context.Context, path string) error {
	existing, err := r.tags.read(path)
	if err != nil {
		return fmt.Errorf("failed to read existing tags: %w", err)
	}

	rawTitle := firstTag(existing, taglib.Title)
	rawArtist := firstTag(existing, taglib.Artist)
	rawAlbum := firstTag(existing, taglib.Album)

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

	// Try acoustic fingerprinting first for a definitive identification.
	if r.fingerprinter != nil {
		if info, found, err := r.fingerprinter.LookupByFile(ctx, path, query.Album); err == nil && found {
			r.logger.Debug("  Fingerprint match: %q by %q", info.Title, info.Artist)
			info, art := r.complete(ctx, src, match{info: info, providerIdx: -1})
			return r.writeResolved(path, existing, info, art)
		}
	}

	m, ok := r.findPrimaryMatch(ctx, src)
	if !ok {
		r.logger.Debug("  No candidate above threshold %.2f, keeping original tags", r.threshold)
		if artist := albumArtistFallback(existing, ""); artist != "" {
			if err := r.tags.write(path, map[string][]string{taglib.AlbumArtist: {artist}}); err != nil {
				r.logger.Warn("  writing album artist to %s: %v", path, err)
			}
		}
		return nil
	}

	info, art := r.complete(ctx, src, m)
	if m.donor {
		info = asVariant(info, m.base, src.version)
	}
	return r.writeResolved(path, existing, info, art)
}

// fileDuration reads the file's own length. Zero means unknown, which disables
// the duration check instead of failing the file.
func (r *Resolver) fileDuration(path string) time.Duration {
	props, err := taglib.ReadProperties(path)
	if err != nil {
		r.logger.Debug("  could not read length: %v", err)
		return 0
	}
	return props.Length
}

// writeResolved writes the resolved metadata, keeping the track and disc
// numbers the file already had, then embeds the artwork if there is one.
// existing are the file's tags as read before resolving it.
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

// findPrimaryMatch asks the providers in order. An exact candidate above the
// threshold ends the search at once, as before. A donor is settled for only
// once every provider has had the chance to offer the variant itself, and then
// the earliest provider's wins: only files that declare a variant pay for the
// extra lookups.
func (r *Resolver) findPrimaryMatch(ctx context.Context, src source) (match, bool) {
	var donor *match
	for i, p := range r.providers {
		results, err := p.Search(ctx, src.query)
		if err != nil {
			r.logger.Debug("  provider %s failed: %v", p.Name(), err)
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

// fillGaps queries the providers after the primary's to fill its missing
// fields. A filler passes the same constraints as the match it completes.
// Artwork URLs in failedArtwork count as missing, wherever they come from.
func (r *Resolver) fillGaps(ctx context.Context, src source, primary match, failedArtwork map[string]bool) TrackInfo {
	base := primary.info
	if failedArtwork[base.ArtworkURL] {
		base.ArtworkURL = ""
	}
	if !hasMissingFields(base) {
		return base
	}

	for _, p := range r.providers[primary.providerIdx+1:] {
		results, err := p.Search(ctx, src.query)
		if err != nil {
			r.logger.Debug("  provider %s failed: %v", p.Name(), err)
			continue
		}
		if len(results) == 0 {
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

		if !hasMissingFields(base) {
			break
		}
	}

	return base
}

// hasMissingFields returns true if any gap-fillable field is empty/zero.
func hasMissingFields(t TrackInfo) bool {
	return t.Genre == "" ||
		t.TrackNumber == 0 ||
		t.DiscNumber == 0 ||
		t.Year == 0 ||
		t.ISRC == "" ||
		t.ArtworkURL == ""
}

// mergeTrackInfo copies gap-fillable fields from filler into base where base has zero values.
// Authoritative fields (Title, Artist, Album, AlbumArtist) are never overwritten.
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
