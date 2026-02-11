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
type Resolver struct {
	provider  Provider
	logger    *logger.Logger
	threshold float64
}

// NewResolver creates a new Resolver with the given provider.
// If threshold is 0, the default (0.7) is used.
func NewResolver(p Provider, log *logger.Logger, threshold float64) *Resolver {
	if threshold <= 0 {
		threshold = defaultConfidenceThreshold
	}
	return &Resolver{
		provider:  p,
		logger:    log,
		threshold: threshold,
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
	// Read existing tags from file
	existingTags, err := taglib.ReadTags(path)
	if err != nil {
		return fmt.Errorf("failed to read existing tags: %w", err)
	}

	rawTitle := firstTag(existingTags, taglib.Title)
	rawArtist := firstTag(existingTags, taglib.Artist)

	if rawTitle == "" {
		r.logger.Debug("  Skipping: no title metadata")
		return nil
	}

	// Normalize
	query := NormalizeQuery(rawTitle, rawArtist)
	r.logger.Debug("  Normalized: title=%q artist=%q", query.Title, query.Artist)

	if query.Title == "" {
		return nil
	}

	// Search provider
	results, err := r.provider.Search(ctx, query)
	if err != nil {
		return fmt.Errorf("provider search failed: %w", err)
	}
	if len(results) == 0 {
		r.logger.Debug("  No results from %s", r.provider.Name())
		return nil
	}

	// Score and pick best match
	best := results[0]
	best.Confidence = score(query, best)

	for _, result := range results[1:] {
		result.Confidence = score(query, result)
		if result.Confidence > best.Confidence {
			best = result
		}
	}

	r.logger.Debug("  Best match: %q by %q (confidence: %.2f)", best.Title, best.Artist, best.Confidence)

	if best.Confidence < r.threshold {
		r.logger.Debug("  Confidence %.2f below threshold %.2f, keeping original tags", best.Confidence, r.threshold)
		return nil
	}

	// Write metadata
	if err := WriteTags(path, best); err != nil {
		return fmt.Errorf("failed to write tags: %w", err)
	}

	// Download and embed artwork
	if best.ArtworkURL != "" {
		if err := r.downloadAndEmbedArtwork(ctx, path, best.ArtworkURL); err != nil {
			r.logger.Warn("  Failed to embed artwork: %v", err)
		}
	}

	return nil
}

func (r *Resolver) downloadAndEmbedArtwork(ctx context.Context, filePath, artworkURL string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, artworkURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create artwork request: %w", err)
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to download artwork: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("artwork download returned %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read artwork data: %w", err)
	}

	return WriteArtwork(filePath, data)
}

// score computes a similarity score (0.0-1.0) between the query and a result.
func score(query SearchQuery, result TrackInfo) float64 {
	titleScore := similarity(normalize(query.Title), normalize(result.Title))
	artistScore := similarity(normalize(query.Artist), normalize(result.Artist))

	if query.Artist == "" {
		return titleScore
	}
	// Weight: 60% title, 40% artist
	return titleScore*0.6 + artistScore*0.4
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
