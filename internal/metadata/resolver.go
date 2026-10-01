package metadata

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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
	tags               *tagStore
	workers            int // files resolved at once in the per-file phase
}

// NewResolver creates a new Resolver with the given providers.
// If threshold is 0, the default (0.7) is used.
func NewResolver(providers []Provider, log *logger.Logger, threshold float64) *Resolver {
	if threshold <= 0 {
		threshold = defaultConfidenceThreshold
	}
	cached := make([]Provider, len(providers))
	for i, p := range providers {
		cached[i] = &cachedProvider{Provider: p}
	}
	return &Resolver{
		providers:  cached,
		logger:     log,
		threshold:  threshold,
		httpClient: &http.Client{Timeout: 15 * time.Second},
		tags:       &tagStore{},
		workers:    1,
	}
}

// WithWorkers sets how many files the per-file phase resolves at once.
// Returns the same Resolver to allow chaining.
func (r *Resolver) WithWorkers(n int) *Resolver {
	r.workers = max(n, 1)
	return r
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

	groups := r.groupByAlbum(files)
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

	failed := r.resolveFiles(ctx, files)
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("resolving metadata: %w", err)
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

// resolveFiles runs the per-file phase on a pool of workers and returns how
// many files failed. The phases before it stay sequential: the MusicBrainz
// rate limit would serialise them anyway.
func (r *Resolver) resolveFiles(ctx context.Context, files []string) int {
	indexes := make(chan int)
	var failed atomic.Int32
	var wg sync.WaitGroup

	for range min(r.workers, len(files)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range indexes {
				// The feeder checks too, but once a cancel and a waiting
				// worker are both ready its select may pick either.
				if ctx.Err() != nil {
					continue
				}
				if err := r.forFile(i, len(files)).resolveSafely(ctx, files[i]); err != nil {
					failed.Add(1)
				}
			}
		}()
	}

	for i := range files {
		if ctx.Err() != nil {
			break
		}
		select {
		case indexes <- i:
		case <-ctx.Done():
		}
	}
	close(indexes)
	wg.Wait()

	return int(failed.Load())
}

// forFile returns a copy of the resolver whose log lines carry the file's
// position, so the lines of files resolved side by side can be told apart.
// Everything else is shared: providers, caches, the tag store.
func (r *Resolver) forFile(i, n int) *Resolver {
	fr := *r
	fr.logger = r.logger.WithPrefix(fmt.Sprintf("%d/%d", i+1, n))
	return &fr
}

// resolveSafely resolves one file and turns a panic into its failure. The
// workers run off any handler stack: a panic there would take the whole
// process down, and with it every job of the web server.
func (r *Resolver) resolveSafely(ctx context.Context, path string) (err error) {
	defer func() {
		if p := recover(); p != nil {
			r.logger.Error("panic while resolving %s: %v", path, p)
			err = fmt.Errorf("panic while resolving %s: %v", path, p)
		}
	}()

	r.logger.Debug("Processing: %s", path)
	if err := r.resolveFile(ctx, path); err != nil {
		r.logger.Warn("Failed to resolve metadata: %v", err)
		return err
	}
	return nil
}

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

// complete fills the match's gaps and downloads its artwork. A URL that
// yields no image is set aside and gap filling runs again without it, so the
// next provider's artwork gets its chance: MusicBrainz hands out Cover Art
// Archive URLs unchecked, and offers a failed one again as a filler when its
// text search lands on the same release. Searches are remembered for the run,
// so running again sends no request.
func (r *Resolver) complete(ctx context.Context, src source, m match) (TrackInfo, []byte) {
	failed := make(map[string]bool)
	for {
		info := r.fillGaps(ctx, src, m, failed)
		if info.ArtworkURL == "" {
			if len(failed) > 0 {
				r.logger.Warn("  No artwork could be downloaded: %d URLs failed", len(failed))
			}
			return info, nil
		}

		art, err := r.downloadArtwork(ctx, info.ArtworkURL)
		if err == nil {
			return info, art
		}
		r.logger.Debug("  dropping artwork %s: %v", info.ArtworkURL, err)
		failed[info.ArtworkURL] = true
	}
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

// albumArtistFallback returns the album artist to write when the file would
// otherwise be left without one: the primary artist, the first before a comma.
// Music servers such as Navidrome otherwise file every track with a featured
// artist under an entry of its own. artist is the artist about to be written,
// empty when none is.
func albumArtistFallback(existing map[string][]string, artist string) string {
	if firstTag(existing, taglib.AlbumArtist) != "" {
		return ""
	}
	if artist == "" {
		artist = firstTag(existing, taglib.Artist)
	}
	if i := strings.Index(artist, ","); i > 0 {
		artist = strings.TrimSpace(artist[:i])
	}
	return artist
}

// downloadArtwork fetches the image at artworkURL. An empty body counts as a
// failure: it is no more an image than a 404.
func (r *Resolver) downloadArtwork(ctx context.Context, artworkURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, artworkURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create artwork request: %w", err)
	}

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to download artwork: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("artwork download returned %d", resp.StatusCode)
	}

	const maxArtworkSize = 10 << 20 // 10 MB
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxArtworkSize))
	if err != nil {
		return nil, fmt.Errorf("failed to read artwork data: %w", err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("artwork download returned no data")
	}
	return data, nil
}

const trackMatchThreshold = 0.6

// resolveGroup looks up the full tracklist for album via ar and writes
// TrackNumber/DiscNumber to each file whose title matches a tracklist entry
// with sufficient confidence.
func (r *Resolver) resolveGroup(ctx context.Context, album string, files []string, ar AlbumResolver) error {
	artist := ""
	if len(files) > 0 {
		if tags, err := r.tags.read(files[0]); err == nil {
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
		tags, err := r.tags.read(path)
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
		if err := r.tags.writePositional(path, track.TrackNumber, track.DiscNumber); err != nil {
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
		tags, err := r.tags.read(path)
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
		if err := r.tags.writePositional(path, track.TrackNumber, track.DiscNumber); err != nil {
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
func (r *Resolver) groupByAlbum(files []string) map[string][]string {
	groups := make(map[string][]string)
	for _, path := range files {
		tags, err := r.tags.read(path)
		if err != nil {
			continue
		}
		album := firstTag(tags, taglib.Album)
		groups[album] = append(groups[album], path)
	}
	return groups
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

// mergeWithExisting keeps the non-zero TrackNumber and DiscNumber the file
// already has over whatever the provider returned. This prevents a wrong
// release selection from overwriting correct positional data from yt-dlp or
// from the album-first phases.
func mergeWithExisting(tags map[string][]string, info TrackInfo) TrackInfo {
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
