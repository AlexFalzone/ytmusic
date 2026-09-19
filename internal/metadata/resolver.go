package metadata

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"ytmusic/internal/logger"

	"go.senan.xyz/taglib"
)

const defaultConfidenceThreshold = 0.7

// Resolver orchestrates metadata resolution: reads existing tags, normalizes,
// searches providers, scores results, and writes back the best metadata.
// When multiple providers are configured, the Resolver tries them in order for
// the primary match (fallback) and then fills missing fields from the remaining
// providers (gap filling).
type Resolver struct {
	providers          []Provider
	logger             *logger.Logger
	threshold          float64
	fingerprinter      Fingerprinter      // nil if not configured
	albumResolver      AlbumResolver      // nil if not configured
	batchFingerprinter BatchFingerprinter // nil if not configured
	releaseResolver    ReleaseResolver    // nil if not configured
	httpClient         *http.Client
}

// NewResolver creates a new Resolver with the given providers.
// If threshold is 0, the default (0.7) is used.
func NewResolver(providers []Provider, log *logger.Logger, threshold float64) *Resolver {
	if threshold <= 0 {
		threshold = defaultConfidenceThreshold
	}
	return &Resolver{
		providers:  providers,
		logger:     log,
		threshold:  threshold,
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
}

// WithFingerprinter attaches an audio fingerprinter for pre-search identification.
// Returns the same Resolver to allow chaining.
func (r *Resolver) WithFingerprinter(f Fingerprinter) *Resolver {
	r.fingerprinter = f
	return r
}

// WithAlbumResolver attaches an album resolver for the album-first positional-tag phase.
// Returns the same Resolver to allow chaining.
func (r *Resolver) WithAlbumResolver(ar AlbumResolver) *Resolver {
	r.albumResolver = ar
	return r
}

// WithBatchFingerprinter attaches a batch fingerprinter for the batch-fingerprint phase.
func (r *Resolver) WithBatchFingerprinter(bf BatchFingerprinter) *Resolver {
	r.batchFingerprinter = bf
	return r
}

// WithReleaseResolver attaches a release resolver used by findDominantRelease.
func (r *Resolver) WithReleaseResolver(rr ReleaseResolver) *Resolver {
	r.releaseResolver = rr
	return r
}

// Resolve processes a list of audio file paths: for each file, it reads existing
// metadata, normalizes it, searches the provider, scores the best match, and
// writes improved metadata back if confident enough.
func (r *Resolver) Resolve(ctx context.Context, files []string) error {
	r.logger.Info("resolving metadata for %d files", len(files))

	groups := groupByAlbum(files)
	resolvedByA := make(map[string]bool)

	// Phase A: batch fingerprint → dominant release (writes positional tags)
	if r.batchFingerprinter != nil && r.releaseResolver != nil {
		for album, group := range groups {
			if album == "" || len(group) < 2 {
				continue
			}
			for _, p := range r.resolveGroupByFingerprint(ctx, group) {
				resolvedByA[p] = true
			}
		}
	}

	// Phase B: album-first text search (skips files already resolved by Phase A)
	if r.albumResolver != nil {
		for album, group := range groups {
			if album == "" {
				continue
			}
			unresolved := filterResolved(group, resolvedByA)
			if len(unresolved) == 0 {
				continue
			}
			if err := r.resolveGroup(ctx, album, unresolved, r.albumResolver); err != nil {
				r.logger.Warn("album-first phase failed for %q: %v", album, err)
			}
		}
	}

	var failed int
	for i, path := range files {
		select {
		case <-ctx.Done():
			return fmt.Errorf("resolving metadata: %w", ctx.Err())
		default:
		}

		r.logger.Debug("[%d/%d] Processing: %s", i+1, len(files), path)

		if err := r.resolveFile(ctx, path); err != nil {
			r.logger.Warn("[%d/%d] Failed to resolve metadata: %v", i+1, len(files), err)
			failed++
		}
	}

	if failed == len(files) {
		return fmt.Errorf("all %d files failed metadata resolution", len(files))
	}

	if failed > 0 {
		r.logger.Warn("%d of %d files failed metadata resolution", failed, len(files))
	}

	r.logger.Info("Metadata resolution completed")
	return nil
}

func (r *Resolver) resolveFile(ctx context.Context, path string) error {
	existingTags, err := taglib.ReadTags(path)
	if err != nil {
		return fmt.Errorf("failed to read existing tags: %w", err)
	}

	rawTitle := firstTag(existingTags, taglib.Title)
	rawArtist := firstTag(existingTags, taglib.Artist)
	rawAlbum := firstTag(existingTags, taglib.Album)

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
			info = r.fillGaps(ctx, src, match{info: info, providerIdx: -1})
			return r.writeResolved(ctx, path, info)
		}
	}

	m, ok := r.findPrimaryMatch(ctx, src)
	if !ok {
		r.logger.Debug("  No candidate above threshold %.2f, keeping original tags", r.threshold)
		ensureAlbumArtist(path)
		return nil
	}

	return r.writeResolved(ctx, path, r.fillGaps(ctx, src, m))
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
// numbers the file already had, then embeds the artwork.
func (r *Resolver) writeResolved(ctx context.Context, path string, info TrackInfo) error {
	info = mergeWithExisting(path, info)
	if err := WriteTags(path, info); err != nil {
		return fmt.Errorf("failed to write tags: %w", err)
	}
	if info.ArtworkURL != "" {
		if err := r.downloadAndEmbedArtwork(ctx, path, info.ArtworkURL); err != nil {
			r.logger.Warn("  Failed to embed artwork: %v", err)
		}
	}
	ensureAlbumArtist(path)
	return nil
}

// findPrimaryMatch asks the providers in order and returns the first candidate
// above the threshold.
func (r *Resolver) findPrimaryMatch(ctx context.Context, src source) (match, bool) {
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

		best := r.evaluate(src, results)
		if best == nil {
			continue
		}
		r.logger.Debug("  %s: best %q by %q (confidence: %.2f)", p.Name(), best.info.Title, best.info.Artist, best.info.Confidence)
		if best.info.Confidence >= r.threshold {
			best.providerIdx = i
			return *best, true
		}
	}
	return match{}, false
}

// fillGaps queries the providers after the primary's to fill its missing
// fields. A filler passes the same constraints as the match it completes.
func (r *Resolver) fillGaps(ctx context.Context, src source, primary match) TrackInfo {
	base := primary.info
	if !hasMissingFields(base) {
		return base
	}

	for _, p := range r.providers[primary.providerIdx+1:] {
		results, err := p.Search(ctx, src.query)
		if err != nil || len(results) == 0 {
			continue
		}

		filler := r.evaluate(src, results)
		if filler == nil || filler.info.Confidence < r.threshold {
			continue
		}

		r.logger.Debug("  gap fill from %s: %q by %q", p.Name(), filler.info.Title, filler.info.Artist)
		base = mergeTrackInfo(base, filler.info)

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

// ensureAlbumArtist sets AlbumArtist to the primary artist (first before comma)
// if it's missing. This prevents music servers like Navidrome from creating
// separate entries for featured tracks.
func ensureAlbumArtist(path string) {
	tags, err := taglib.ReadTags(path)
	if err != nil {
		return
	}

	if firstTag(tags, taglib.AlbumArtist) != "" {
		return
	}

	artist := firstTag(tags, taglib.Artist)
	if artist == "" {
		return
	}

	if i := strings.Index(artist, ","); i > 0 {
		artist = strings.TrimSpace(artist[:i])
	}

	taglib.WriteTags(path, map[string][]string{
		taglib.AlbumArtist: {artist},
	}, 0)
}

func (r *Resolver) downloadAndEmbedArtwork(ctx context.Context, filePath, artworkURL string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, artworkURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create artwork request: %w", err)
	}

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to download artwork: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("artwork download returned %d", resp.StatusCode)
	}

	const maxArtworkSize = 10 << 20 // 10 MB
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxArtworkSize))
	if err != nil {
		return fmt.Errorf("failed to read artwork data: %w", err)
	}

	return WriteArtwork(filePath, data)
}

const trackMatchThreshold = 0.6

// resolveGroup looks up the full tracklist for album via ar and writes
// TrackNumber/DiscNumber to each file whose title matches a tracklist entry
// with sufficient confidence.
func (r *Resolver) resolveGroup(ctx context.Context, album string, files []string, ar AlbumResolver) error {
	artist := ""
	if len(files) > 0 {
		if tags, err := taglib.ReadTags(files[0]); err == nil {
			artist = firstTag(tags, taglib.Artist)
		}
	}

	tl, found, err := ar.ResolveAlbum(ctx, album, artist)
	if err != nil {
		return fmt.Errorf("resolve album %q: %w", album, err)
	}
	if !found || len(tl.Tracks) == 0 {
		return nil
	}

	for _, path := range files {
		tags, err := taglib.ReadTags(path)
		if err != nil {
			continue
		}
		title := firstTag(tags, taglib.Title)
		if title == "" {
			continue
		}

		track, matchScore := matchTrackByTitle(title, tl.Tracks)
		if matchScore < trackMatchThreshold {
			r.logger.Debug("  album-first: low match %.2f for %q, skipping", matchScore, title)
			continue
		}

		r.logger.Debug("  album-first: %q → track %d disc %d (score %.2f)", title, track.TrackNumber, track.DiscNumber, matchScore)
		if err := writePositionalTags(path, track.TrackNumber, track.DiscNumber); err != nil {
			r.logger.Warn("  album-first: failed to write positional tags for %q: %v", path, err)
		}
	}

	return nil
}

// resolveGroupByFingerprint fingerprints all files in the group, finds the dominant
// release (the one appearing in >= 50% of recording lookups), then writes positional
// tags for each file whose title matches a tracklist entry with sufficient confidence.
// Returns the paths of files that were successfully resolved.
func (r *Resolver) resolveGroupByFingerprint(ctx context.Context, files []string) []string {
	matches := r.batchFingerprinter.BatchLookupByFiles(ctx, files)
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

	pathToMBID := make(map[string]string, len(matches))
	for _, m := range matches {
		pathToMBID[m.Path] = m.MBID
	}

	var resolved []string
	for _, path := range files {
		tags, err := taglib.ReadTags(path)
		if err != nil {
			continue
		}
		title := firstTag(tags, taglib.Title)
		if title == "" {
			continue
		}

		track, matchScore := matchTrackByTitle(title, tl.Tracks)
		if matchScore < trackMatchThreshold {
			r.logger.Debug("  batch fingerprint: low match %.2f for %q, skipping", matchScore, title)
			continue
		}

		r.logger.Debug("  batch fingerprint: %q → track %d disc %d (score %.2f)", title, track.TrackNumber, track.DiscNumber, matchScore)
		if err := writePositionalTags(path, track.TrackNumber, track.DiscNumber); err != nil {
			r.logger.Warn("  batch fingerprint: failed to write positional tags for %q: %v", path, err)
			continue
		}
		resolved = append(resolved, path)
	}

	return resolved
}

// findDominantRelease fetches the release IDs for each recording MBID and returns
// the release ID that appears in >= 50% of recordings. Returns ("", false) if no
// release reaches the quorum.
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
		// Strict > for count; lexicographic < on ID breaks ties deterministically.
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

// filterResolved returns files from the slice that are not in the resolved set.
func filterResolved(files []string, resolved map[string]bool) []string {
	var out []string
	for _, f := range files {
		if !resolved[f] {
			out = append(out, f)
		}
	}
	return out
}

// groupByAlbum reads the album tag of each file and groups paths by album name.
func groupByAlbum(files []string) map[string][]string {
	groups := make(map[string][]string)
	for _, path := range files {
		tags, err := taglib.ReadTags(path)
		if err != nil {
			continue
		}
		album := firstTag(tags, taglib.Album)
		groups[album] = append(groups[album], path)
	}
	return groups
}

// writePositionalTags writes TrackNumber and DiscNumber to a file, skipping zeros.
func writePositionalTags(path string, trackNum, discNum int) error {
	tags := make(map[string][]string)
	if trackNum > 0 {
		tags[taglib.TrackNumber] = []string{strconv.Itoa(trackNum)}
	}
	if discNum > 0 {
		tags[taglib.DiscNumber] = []string{strconv.Itoa(discNum)}
	}
	if len(tags) == 0 {
		return nil
	}
	return taglib.WriteTags(path, tags, 0)
}

// matchTrackByTitle finds the track in tracks whose title best matches fileTitle.
// Returns the best match and its similarity score (0.0–1.0).
func matchTrackByTitle(fileTitle string, tracks []ReleaseTrack) (ReleaseTrack, float64) {
	best := tracks[0]
	bestScore := similarity(normalize(fileTitle), normalize(tracks[0].Title))
	for _, t := range tracks[1:] {
		s := similarity(normalize(fileTitle), normalize(t.Title))
		if s > bestScore {
			bestScore = s
			best = t
		}
	}
	return best, bestScore
}

// mergeWithExisting reads the file's current TrackNumber and DiscNumber tags and
// keeps their non-zero values over whatever the provider returned. This prevents
// a wrong release selection from overwriting correct positional data from yt-dlp.
func mergeWithExisting(path string, info TrackInfo) TrackInfo {
	tags, err := taglib.ReadTags(path)
	if err != nil {
		return info
	}
	if n := parseTagInt(tags, taglib.TrackNumber); n > 0 {
		info.TrackNumber = n
	}
	if n := parseTagInt(tags, taglib.DiscNumber); n > 0 {
		info.DiscNumber = n
	}
	return info
}

// parseTagInt reads a tag value as an integer. Returns 0 if absent or non-numeric.
func parseTagInt(tags map[string][]string, key string) int {
	s := firstTag(tags, key)
	if s == "" {
		return 0
	}
	// Handle "5/12" format (track number / total tracks) written by some taggers.
	if i := strings.Index(s, "/"); i > 0 {
		s = strings.TrimSpace(s[:i])
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

func firstTag(tags map[string][]string, key string) string {
	if vals, ok := tags[key]; ok && len(vals) > 0 {
		return vals[0]
	}
	return ""
}
