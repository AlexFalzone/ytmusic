package itunes

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"ytmusic/internal/httpjson"
	"ytmusic/internal/metadata"
	"ytmusic/internal/throttle"
)

// Apple's limit is about 20 calls a minute; past it the genre, often only iTunes has, silently goes.
const requestInterval = 3 * time.Second

type Client struct {
	api    *httpjson.Client
	apiURL string
}

func New() *Client {
	return &Client{
		api: &httpjson.Client{
			HTTP:     &http.Client{Timeout: 10 * time.Second},
			Throttle: throttle.New(requestInterval),
		},
		apiURL: "https://itunes.apple.com/search",
	}
}

func (c *Client) Name() string { return "itunes" }

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

	var searchResp searchResponse
	if err := c.api.Get(ctx, c.apiURL+"?"+params.Encode(), nil, &searchResp); err != nil {
		return nil, fmt.Errorf("itunes search: %w", err)
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
		info := metadata.TrackInfo{
			Title:       item.TrackName,
			Artist:      item.ArtistName,
			Album:       item.CollectionName,
			AlbumArtist: item.ArtistName,
			Genre:       item.PrimaryGenreName,
			TrackNumber: item.TrackNumber,
			DiscNumber:  item.DiscNumber,
			Year:        metadata.ParseYear(item.ReleaseDate),
			ReleaseDate: item.ReleaseDate,
			ArtworkURL:  strings.Replace(item.ArtworkURL100, "100x100", "600x600", 1),
			Duration:    time.Duration(item.TrackTimeMillis) * time.Millisecond,
		}
		results = append(results, info)
	}
	return results
}

type searchResponse struct {
	Results []resultItem `json:"results"`
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
