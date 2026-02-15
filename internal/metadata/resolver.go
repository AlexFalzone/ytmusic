package metadata

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode"

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
	providers  []Provider
	logger     *logger.Logger
	threshold  float64
	httpClient *http.Client
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

// Resolve processes a list of audio file paths: for each file, it reads existing
// metadata, normalizes it, searches the provider, scores the best match, and
// writes improved metadata back if confident enough.
func (r *Resolver) Resolve(ctx context.Context, files []string) error {
	r.logger.Info("=== Resolving metadata for %d files ===", len(files))

	var failed int
	for i, path := range files {
		select {
		case <-ctx.Done():
			return fmt.Errorf("metadata resolution cancelled")
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

	query := NormalizeQuery(rawTitle, rawArtist)
	query.Album = strings.TrimSpace(rawAlbum)
	r.logger.Debug("  Normalized: title=%q artist=%q album=%q", query.Title, query.Artist, query.Album)

	if query.Title == "" {
		return nil
	}

	best, matchIdx := r.findPrimaryMatch(ctx, query)

	if best.Confidence < r.threshold {
		r.logger.Debug("  Confidence %.2f below threshold %.2f, keeping original tags", best.Confidence, r.threshold)
		ensureAlbumArtist(path)
		return nil
	}

	best = r.fillGaps(ctx, query, best, matchIdx)

	if err := WriteTags(path, best); err != nil {
		return fmt.Errorf("failed to write tags: %w", err)
	}

	if best.ArtworkURL != "" {
		if err := r.downloadAndEmbedArtwork(ctx, path, best.ArtworkURL); err != nil {
			r.logger.Warn("  Failed to embed artwork: %v", err)
		}
	}

	ensureAlbumArtist(path)
	return nil
}

// findPrimaryMatch tries providers in order until one returns a match above threshold.
func (r *Resolver) findPrimaryMatch(ctx context.Context, query SearchQuery) (TrackInfo, int) {
	var best TrackInfo
	var matchIdx int
	for i, p := range r.providers {
		results, err := p.Search(ctx, query)
		if err != nil {
			r.logger.Debug("  provider %s failed: %v", p.Name(), err)
			continue
		}
		if len(results) == 0 {
			r.logger.Debug("  No results from %s", p.Name())
			continue
		}

		candidate := pickBest(query, results)
		r.logger.Debug("  %s: best %q by %q (confidence: %.2f)", p.Name(), candidate.Title, candidate.Artist, candidate.Confidence)

		if candidate.Confidence >= r.threshold {
			return candidate, i
		}
		if candidate.Confidence > best.Confidence {
			best = candidate
			matchIdx = i
		}
	}
	return best, matchIdx
}

// pickBest scores all results and returns the one with the highest confidence.
func pickBest(query SearchQuery, results []TrackInfo) TrackInfo {
	best := results[0]
	best.Confidence = score(query, best)
	for _, r := range results[1:] {
		r.Confidence = score(query, r)
		if r.Confidence > best.Confidence {
			best = r
		}
	}
	return best
}

// fillGaps queries remaining providers to fill missing fields in the primary match.
func (r *Resolver) fillGaps(ctx context.Context, query SearchQuery, base TrackInfo, fromIdx int) TrackInfo {
	if !hasMissingFields(base) {
		return base
	}

	for _, p := range r.providers[fromIdx+1:] {
		results, err := p.Search(ctx, query)
		if err != nil || len(results) == 0 {
			continue
		}

		filler := pickBest(query, results)
		if filler.Confidence < r.threshold {
			continue
		}

		r.logger.Debug("  gap fill from %s: %q by %q", p.Name(), filler.Title, filler.Artist)
		base = mergeTrackInfo(base, filler)

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

// similarity returns how similar two strings are (0.0-1.0).
// Uses both token overlap and compact string comparison to handle cases
// like "theweeknd" vs "the weeknd".
func similarity(a, b string) float64 {
	if a == "" && b == "" {
		return 1.0
	}
	if a == "" || b == "" {
		return 0.0
	}

	// Check compact (no-space) equality first: handles "theweeknd" == "the weeknd"
	compactA := strings.ReplaceAll(a, " ", "")
	compactB := strings.ReplaceAll(b, " ", "")
	if compactA == compactB {
		return 1.0
	}

	// Token overlap
	tokensA := tokenize(a)
	tokensB := tokenize(b)

	if len(tokensA) == 0 || len(tokensB) == 0 {
		return 0.0
	}

	setB := make(map[string]bool, len(tokensB))
	for _, t := range tokensB {
		setB[t] = true
	}

	matches := 0
	for _, t := range tokensA {
		if setB[t] {
			matches++
		}
	}

	maxLen := len(tokensA)
	if len(tokensB) > maxLen {
		maxLen = len(tokensB)
	}
	return float64(matches) / float64(maxLen)
}

// normalize lowercases and strips non-alphanumeric characters for comparison.
func normalize(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsSpace(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// tokenize splits a string into lowercase tokens.
func tokenize(s string) []string {
	fields := strings.Fields(s)
	var result []string
	for _, f := range fields {
		if f != "" {
			result = append(result, f)
		}
	}
	return result
}

func firstTag(tags map[string][]string, key string) string {
	if vals, ok := tags[key]; ok && len(vals) > 0 {
		return vals[0]
	}
	return ""
}
