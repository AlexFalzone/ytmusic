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

// requestInterval keeps to Apple's documented limit of about 20 calls a
// minute. Exceeding it gets requests refused, and gap filling then loses the
// genre without a word: iTunes is often the only provider that has one.
const requestInterval = 3 * time.Second

// Client is an iTunes Search API client that implements metadata.Provider.
type Client struct {
	api    *httpjson.Client
	apiURL string
}

// New creates a new iTunes client.
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

		info.ReleaseDate = item.ReleaseDate
		info.Year = metadata.ParseYear(item.ReleaseDate)

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
