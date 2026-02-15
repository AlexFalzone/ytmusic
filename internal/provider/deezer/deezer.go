package deezer

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

// Client is a Deezer API client that implements metadata.Provider.
type Client struct {
	httpClient *http.Client
	apiURL     string
}

// New creates a new Deezer client.
func New() *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 10 * time.Second},
		apiURL:     "https://api.deezer.com",
	}
}

func (c *Client) Name() string { return "deezer" }

// Search queries the Deezer search API and returns matching tracks.
func (c *Client) Search(ctx context.Context, query metadata.SearchQuery) ([]metadata.TrackInfo, error) {
	q := buildQuery(query)
	if q == "" {
		return nil, nil
	}

	reqURL := fmt.Sprintf("%s/search?q=%s&limit=5", c.apiURL, url.QueryEscape(q))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create deezer request: %w", err)
	}
	req.Header.Set("User-Agent", "ytmusic/1.0")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("deezer search request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("deezer search returned %d: %s", resp.StatusCode, body)
	}

	var searchResp searchResponse
	if err := json.NewDecoder(resp.Body).Decode(&searchResp); err != nil {
		return nil, fmt.Errorf("failed to decode deezer response: %w", err)
	}

	if searchResp.Error != nil {
		return nil, fmt.Errorf("deezer API error: %s", searchResp.Error.Message)
	}

	return parseResults(searchResp.Data), nil
}

func buildQuery(query metadata.SearchQuery) string {
	escape := func(s string) string {
		return strings.ReplaceAll(s, "\"", "")
	}
	var parts []string
	if query.Title != "" {
		parts = append(parts, "track:\""+escape(query.Title)+"\"")
	}
	if query.Artist != "" {
		parts = append(parts, "artist:\""+escape(query.Artist)+"\"")
	}
	if query.Album != "" {
		parts = append(parts, "album:\""+escape(query.Album)+"\"")
	}
	return strings.Join(parts, " ")
}

func parseResults(items []trackItem) []metadata.TrackInfo {
	var results []metadata.TrackInfo
	for _, item := range items {
		var artworkURL string
		if item.Album.CoverXL != "" {
			artworkURL = item.Album.CoverXL
		} else if item.Album.CoverBig != "" {
			artworkURL = item.Album.CoverBig
		}

		info := metadata.TrackInfo{
			Title:       item.TitleShort,
			Artist:      item.Artist.Name,
			Album:       item.Album.Title,
			AlbumArtist: item.Artist.Name,
			ISRC:        item.ISRC,
			ArtworkURL:  artworkURL,
			Duration:    time.Duration(item.Duration) * time.Second,
		}
		results = append(results, info)
	}
	return results
}

// Deezer API response types

type searchResponse struct {
	Data  []trackItem `json:"data"`
	Error *apiError   `json:"error,omitempty"`
}

type apiError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	Code    int    `json:"code"`
}

type trackItem struct {
	ID           int       `json:"id"`
	Title        string    `json:"title"`
	TitleShort   string    `json:"title_short"`
	TitleVersion string    `json:"title_version"`
	ISRC         string    `json:"isrc"`
	Duration     int       `json:"duration"`
	Artist       artist    `json:"artist"`
	Album        albumInfo `json:"album"`
}

type artist struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type albumInfo struct {
	ID       int    `json:"id"`
	Title    string `json:"title"`
	CoverBig string `json:"cover_big"`
	CoverXL  string `json:"cover_xl"`
}
