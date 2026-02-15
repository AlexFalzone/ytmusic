package itunes

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"ytmusic/internal/metadata"
)

// Client is an iTunes Search API client that implements metadata.Provider.
type Client struct {
	httpClient *http.Client
	apiURL     string
}

// New creates a new iTunes client.
func New() *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 10 * time.Second},
		apiURL:     "https://itunes.apple.com/search",
	}
}

func (c *Client) Name() string { return "itunes" }

// Search queries the iTunes Search API and returns matching tracks.
func (c *Client) Search(ctx context.Context, query metadata.SearchQuery) ([]metadata.TrackInfo, error) {
	term := buildTerm(query)
	if term == "" {
		return nil, nil
	}

	params := url.Values{}
	params.Set("term", term)
	params.Set("media", "music")
	params.Set("entity", "song")
	params.Set("limit", "5")

	reqURL := fmt.Sprintf("%s?%s", c.apiURL, params.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create itunes request: %w", err)
	}
	req.Header.Set("User-Agent", "ytmusic/1.0")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("itunes search request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("itunes search returned %d: %s", resp.StatusCode, body)
	}

	var searchResp searchResponse
	if err := json.NewDecoder(resp.Body).Decode(&searchResp); err != nil {
		return nil, fmt.Errorf("failed to decode itunes response: %w", err)
	}

	return parseResults(searchResp.Results), nil
}

func buildTerm(query metadata.SearchQuery) string {
	var parts []string
	if query.Title != "" {
		parts = append(parts, query.Title)
	}
	if query.Artist != "" {
		parts = append(parts, query.Artist)
	}
	return strings.Join(parts, " ")
}

func parseResults(items []resultItem) []metadata.TrackInfo {
	var results []metadata.TrackInfo
	for _, item := range items {
		artworkURL := item.ArtworkURL100
		// Upgrade to 600x600 artwork
		if artworkURL != "" {
			artworkURL = strings.Replace(artworkURL, "100x100", "600x600", 1)
		}

		info := metadata.TrackInfo{
			Title:       item.TrackName,
			Artist:      item.ArtistName,
			Album:       item.CollectionName,
			AlbumArtist: item.ArtistName,
			Genre:       item.PrimaryGenreName,
			TrackNumber: item.TrackNumber,
			DiscNumber:  item.DiscNumber,
			ArtworkURL:  artworkURL,
			Duration:    time.Duration(item.TrackTimeMillis) * time.Millisecond,
		}

		if item.ReleaseDate != "" {
			info.ReleaseDate = item.ReleaseDate
			if len(item.ReleaseDate) >= 4 {
				fmt.Sscanf(item.ReleaseDate[:4], "%d", &info.Year)
			}
		}

		results = append(results, info)
	}
	return results
}

// iTunes Search API response types

type searchResponse struct {
	ResultCount int          `json:"resultCount"`
	Results     []resultItem `json:"results"`
}

type resultItem struct {
	TrackName        string `json:"trackName"`
	ArtistName       string `json:"artistName"`
	CollectionName   string `json:"collectionName"`
	PrimaryGenreName string `json:"primaryGenreName"`
	TrackNumber      int    `json:"trackNumber"`
	DiscNumber       int    `json:"discNumber"`
	TrackTimeMillis  int    `json:"trackTimeMillis"`
	ArtworkURL100    string `json:"artworkUrl100"`
	ReleaseDate      string `json:"releaseDate"`
}
