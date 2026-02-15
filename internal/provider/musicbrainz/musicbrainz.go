package musicbrainz

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"ytmusic/internal/metadata"
)

// Client is a MusicBrainz Web API client that implements metadata.Provider.
type Client struct {
	httpClient  *http.Client
	apiURL      string
	mu          sync.Mutex
	lastRequest time.Time
}

// New creates a new MusicBrainz client.
func New() *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 10 * time.Second},
		apiURL:     "https://musicbrainz.org/ws/2",
	}
}

func (c *Client) Name() string { return "musicbrainz" }

// Search queries the MusicBrainz recording search API and returns matching tracks.
func (c *Client) Search(ctx context.Context, query metadata.SearchQuery) ([]metadata.TrackInfo, error) {
	q := buildQuery(query)
	if q == "" {
		return nil, nil
	}

	c.rateLimit()

	reqURL := fmt.Sprintf("%s/recording?query=%s&fmt=json&limit=5", c.apiURL, url.QueryEscape(q))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create musicbrainz request: %w", err)
	}
	req.Header.Set("User-Agent", "ytmusic/1.0")
	req.Header.Set("Accept", "application/json")

	resp, err := c.doWithRetry(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("musicbrainz search request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("musicbrainz search returned %d: %s", resp.StatusCode, body)
	}

	var searchResp searchResponse
	if err := json.NewDecoder(resp.Body).Decode(&searchResp); err != nil {
		return nil, fmt.Errorf("failed to decode musicbrainz response: %w", err)
	}

	return parseRecordings(searchResp.Recordings), nil
}

// rateLimit enforces MusicBrainz's 1 request/second limit.
func (c *Client) rateLimit() {
	c.mu.Lock()
	elapsed := time.Since(c.lastRequest)
	c.mu.Unlock()

	if elapsed < time.Second {
		time.Sleep(time.Second - elapsed)
	}

	c.mu.Lock()
	c.lastRequest = time.Now()
	c.mu.Unlock()
}

// doWithRetry executes the request, retrying on 429/503 with backoff.
func (c *Client) doWithRetry(ctx context.Context, req *http.Request) (*http.Response, error) {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable {
		resp.Body.Close()
		retryAfter := 2
		if ra := resp.Header.Get("Retry-After"); ra != "" {
			if parsed, err := strconv.Atoi(ra); err == nil {
				retryAfter = parsed
			}
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Duration(retryAfter) * time.Second):
		}

		c.mu.Lock()
		c.lastRequest = time.Now()
		c.mu.Unlock()
		retry := req.Clone(ctx)
		return c.httpClient.Do(retry)
	}

	return resp, nil
}

func buildQuery(query metadata.SearchQuery) string {
	var parts []string
	if query.Title != "" {
		parts = append(parts, fmt.Sprintf("recording:%q", query.Title))
	}
	if query.Artist != "" {
		parts = append(parts, fmt.Sprintf("artist:%q", query.Artist))
	}
	if query.Album != "" {
		parts = append(parts, fmt.Sprintf("release:%q", query.Album))
	}
	return strings.Join(parts, " AND ")
}

func parseRecordings(recordings []recording) []metadata.TrackInfo {
	var results []metadata.TrackInfo
	for _, rec := range recordings {
		info := metadata.TrackInfo{
			Title:    rec.Title,
			Artist:   joinArtistCredits(rec.ArtistCredit),
			Duration: time.Duration(rec.Length) * time.Millisecond,
		}

		if len(rec.ISRCs) > 0 {
			info.ISRC = rec.ISRCs[0]
		}

		if len(rec.Releases) > 0 {
			rel := pickBestRelease(rec.Releases)
			info.Album = rel.Title
			if len(rel.ArtistCredit) > 0 {
				info.AlbumArtist = rel.ArtistCredit[0].Artist.Name
			}
			info.Year = parseYear(rel.Date)
			info.ReleaseDate = rel.Date
			info.ArtworkURL = fmt.Sprintf("https://coverartarchive.org/release/%s/front-500", rel.ID)

			if len(rel.Media) > 0 && len(rel.Media[0].Track) > 0 {
				if n, err := strconv.Atoi(rel.Media[0].Track[0].Number); err == nil {
					info.TrackNumber = n
				}
			}
		}

		results = append(results, info)
	}
	return results
}

func joinArtistCredits(credits []artistCredit) string {
	var parts []string
	for _, ac := range credits {
		parts = append(parts, ac.Artist.Name)
	}
	return strings.Join(parts, ", ")
}

// pickBestRelease selects the most appropriate release for tagging.
// Prefers: Official status, Album type, no secondary types (not Compilation), earliest date.
func pickBestRelease(releases []release) release {
	best := releases[0]
	bestScore := releaseScore(best)

	for _, rel := range releases[1:] {
		s := releaseScore(rel)
		if s > bestScore || (s == bestScore && rel.Date != "" && (best.Date == "" || rel.Date < best.Date)) {
			best = rel
			bestScore = s
		}
	}
	return best
}

func releaseScore(rel release) int {
	score := 0

	if rel.Status == "Official" {
		score += 4
	}

	if rel.ReleaseGroup.PrimaryType == "Album" {
		score += 2
	}

	if len(rel.ReleaseGroup.SecondaryTypes) == 0 {
		score += 1
	}

	return score
}

func parseYear(date string) int {
	if len(date) >= 4 {
		if y, err := strconv.Atoi(date[:4]); err == nil {
			return y
		}
	}
	return 0
}

// MusicBrainz API response types

type searchResponse struct {
	Recordings []recording `json:"recordings"`
}

type recording struct {
	ID           string         `json:"id"`
	Title        string         `json:"title"`
	Length       int            `json:"length"`
	ArtistCredit []artistCredit `json:"artist-credit"`
	Releases     []release      `json:"releases"`
	ISRCs        []string       `json:"isrcs"`
}

type artistCredit struct {
	Artist artistInfo `json:"artist"`
}

type artistInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type release struct {
	ID           string         `json:"id"`
	Title        string         `json:"title"`
	Status       string         `json:"status"`
	Date         string         `json:"date"`
	ArtistCredit []artistCredit `json:"artist-credit"`
	ReleaseGroup releaseGroup   `json:"release-group"`
	Media        []media        `json:"media"`
}

type releaseGroup struct {
	PrimaryType    string   `json:"primary-type"`
	SecondaryTypes []string `json:"secondary-types"`
}

type media struct {
	Track []track `json:"track"`
}

type track struct {
	Number string `json:"number"`
}
