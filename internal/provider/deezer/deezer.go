package deezer

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"ytmusic/internal/httpjson"
	"ytmusic/internal/metadata"
)

type Client struct {
	api    *httpjson.Client
	apiURL string
}

func New() *Client {
	return &Client{
		api:    &httpjson.Client{HTTP: &http.Client{Timeout: 10 * time.Second}},
		apiURL: "https://api.deezer.com",
	}
}

func (c *Client) Name() string { return "deezer" }

func (c *Client) Search(ctx context.Context, query metadata.SearchQuery) ([]metadata.TrackInfo, error) {
	q := buildQuery(query)
	if q == "" {
		return nil, nil
	}

	var searchResp searchResponse
	reqURL := fmt.Sprintf("%s/search?q=%s&limit=5", c.apiURL, url.QueryEscape(q))
	if err := c.api.Get(ctx, reqURL, nil, &searchResp); err != nil {
		return nil, fmt.Errorf("deezer search: %w", err)
	}
	if searchResp.Error != nil {
		return nil, fmt.Errorf("deezer API error: %s", searchResp.Error.Message)
	}
	return parseResults(searchResp.Data), nil
}

// Free text: the API reads `artist:"…"` as the word "artist" (BUG-14); quotes would force an exact phrase.
func buildQuery(query metadata.SearchQuery) string {
	var parts []string
	for _, field := range []string{query.Title, query.Artist, query.Album} {
		if field = strings.ReplaceAll(field, "\"", ""); field != "" {
			parts = append(parts, field)
		}
	}
	return strings.Join(parts, " ")
}

func parseResults(items []trackItem) []metadata.TrackInfo {
	var results []metadata.TrackInfo
	for _, item := range items {
		artworkURL := item.Album.CoverXL
		if artworkURL == "" {
			artworkURL = item.Album.CoverBig
		}

		info := metadata.TrackInfo{
			Title:       item.TitleShort,
			Artist:      item.Artist.Name,
			Album:       item.Album.Title,
			AlbumArtist: item.Artist.Name,
			TrackNumber: item.TrackPosition,
			DiscNumber:  item.DiskNumber,
			ISRC:        item.ISRC,
			ArtworkURL:  artworkURL,
			Duration:    time.Duration(item.Duration) * time.Second,
		}
		results = append(results, info)
	}
	return results
}

type searchResponse struct {
	Data  []trackItem `json:"data"`
	Error *apiError   `json:"error,omitempty"`
}

type apiError struct {
	Message string `json:"message"`
}

type trackItem struct {
	TitleShort    string    `json:"title_short"`
	ISRC          string    `json:"isrc"`
	Duration      int       `json:"duration"`
	TrackPosition int       `json:"track_position"`
	DiskNumber    int       `json:"disk_number"`
	Artist        artist    `json:"artist"`
	Album         albumInfo `json:"album"`
}

type artist struct {
	Name string `json:"name"`
}

type albumInfo struct {
	Title    string `json:"title"`
	CoverBig string `json:"cover_big"`
	CoverXL  string `json:"cover_xl"`
}
