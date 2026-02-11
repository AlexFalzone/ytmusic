package spotify

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

// Client is a Spotify Web API client that implements the provider.Provider interface.
type Client struct {
	clientID     string
	clientSecret string
	httpClient   *http.Client

	mu          sync.Mutex
	accessToken string
	tokenExpiry time.Time

	cacheMu    sync.Mutex
	genreCache map[string][]string // artist ID → genres

	// Overridable for testing
	tokenURL string
	apiURL   string
}

// New creates a new Spotify client.
func New(clientID, clientSecret string) *Client {
	return &Client{
		clientID:     clientID,
		clientSecret: clientSecret,
		httpClient:   &http.Client{Timeout: 10 * time.Second},
		genreCache:   make(map[string][]string),
		tokenURL:     "https://accounts.spotify.com/api/token",
		apiURL:       "https://api.spotify.com/v1",
	}
}

func (c *Client) Name() string { return "spotify" }

// Search queries the Spotify search API and returns matching tracks.
func (c *Client) Search(ctx context.Context, query metadata.SearchQuery) ([]metadata.TrackInfo, error) {
	q := buildSearchQuery(query)
	if q == "" {
		return nil, nil
	}

	token, err := c.getToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("spotify auth failed: %w", err)
	}

	reqURL := fmt.Sprintf("%s/search?type=track&limit=5&q=%s", c.apiURL, url.QueryEscape(q))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create search request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.doWithRetry(req)
	if err != nil {
		return nil, fmt.Errorf("spotify search request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("spotify search returned %d: %s", resp.StatusCode, body)
	}

	var searchResp searchResponse
	if err := json.NewDecoder(resp.Body).Decode(&searchResp); err != nil {
		return nil, fmt.Errorf("failed to decode spotify response: %w", err)
	}

	results := parseSearchResults(searchResp)

	// Enrich with genres from artist endpoint
	c.enrichGenres(ctx, results, searchResp)

	return results, nil
}

// enrichGenres fetches genres for primary artists and sets them on results.
// Uses an internal cache to avoid redundant API calls.
func (c *Client) enrichGenres(ctx context.Context, results []metadata.TrackInfo, resp searchResponse) {
	for i, item := range resp.Tracks.Items {
		if i >= len(results) || len(item.Artists) == 0 {
			continue
		}

		artistID := item.Artists[0].ID
		if artistID == "" {
			continue
		}

		genres, err := c.getArtistGenres(ctx, artistID)
		if err != nil || len(genres) == 0 {
			continue
		}

		results[i].Genre = formatGenres(genres)
	}
}

// getArtistGenres returns genres for an artist, using cache when available.
func (c *Client) getArtistGenres(ctx context.Context, artistID string) ([]string, error) {
	c.cacheMu.Lock()
	if genres, ok := c.genreCache[artistID]; ok {
		c.cacheMu.Unlock()
		return genres, nil
	}
	c.cacheMu.Unlock()

	token, err := c.getToken(ctx)
	if err != nil {
		return nil, err
	}

	reqURL := fmt.Sprintf("%s/artists/%s", c.apiURL, url.PathEscape(artistID))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.doWithRetry(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("artist request returned %d", resp.StatusCode)
	}

	var artistResp artistResponse
	if err := json.NewDecoder(resp.Body).Decode(&artistResp); err != nil {
		return nil, err
	}

	c.cacheMu.Lock()
	c.genreCache[artistID] = artistResp.Genres
	c.cacheMu.Unlock()

	return artistResp.Genres, nil
}

// formatGenres title-cases and joins genres (max 3).
func formatGenres(genres []string) string {
	limit := 3
	if len(genres) < limit {
		limit = len(genres)
	}
	formatted := make([]string, limit)
	for i := 0; i < limit; i++ {
		formatted[i] = titleCase(genres[i])
	}
	return strings.Join(formatted, ", ")
}

func titleCase(s string) string {
	words := strings.Fields(s)
	for i, w := range words {
		if len(w) > 0 {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(words, " ")
}

func buildSearchQuery(query metadata.SearchQuery) string {
	var parts []string
	if query.Title != "" {
		parts = append(parts, "track:"+query.Title)
	}
	if query.Artist != "" {
		parts = append(parts, "artist:"+query.Artist)
	}
	if query.Album != "" {
		parts = append(parts, "album:"+query.Album)
	}
	return strings.Join(parts, " ")
}

// getToken returns a valid access token, refreshing if necessary.
func (c *Client) getToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.accessToken != "" && time.Now().Before(c.tokenExpiry) {
		return c.accessToken, nil
	}

	data := url.Values{"grant_type": {"client_credentials"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return "", fmt.Errorf("failed to create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(c.clientID, c.clientSecret)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("token request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("token request returned %d: %s", resp.StatusCode, body)
	}

	var tokenResp tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return "", fmt.Errorf("failed to decode token response: %w", err)
	}

	c.accessToken = tokenResp.AccessToken
	// Refresh a bit early to avoid edge-case expiry
	c.tokenExpiry = time.Now().Add(time.Duration(tokenResp.ExpiresIn-60) * time.Second)

	return c.accessToken, nil
}

// doWithRetry executes the request, retrying once on 429.
// Clones the request before retry to avoid issues with consumed bodies.
func (c *Client) doWithRetry(req *http.Request) (*http.Response, error) {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		resp.Body.Close()
		retryAfter := 1
		if ra := resp.Header.Get("Retry-After"); ra != "" {
			if parsed, err := strconv.Atoi(ra); err == nil {
				retryAfter = parsed
			}
		}
		time.Sleep(time.Duration(retryAfter) * time.Second)

		retry := req.Clone(req.Context())
		return c.httpClient.Do(retry)
	}

	return resp, nil
}

func parseSearchResults(resp searchResponse) []metadata.TrackInfo {
	var results []metadata.TrackInfo
	for _, item := range resp.Tracks.Items {
		var artists []string
		for _, a := range item.Artists {
			artists = append(artists, a.Name)
		}

		var albumArtist string
		if len(item.Album.Artists) > 0 {
			albumArtist = item.Album.Artists[0].Name
		}

		var artworkURL string
		if len(item.Album.Images) > 0 {
			artworkURL = item.Album.Images[0].URL
		}

		info := metadata.TrackInfo{
			Title:       item.Name,
			Artist:      strings.Join(artists, ", "),
			Album:       item.Album.Name,
			AlbumArtist: albumArtist,
			TrackNumber: item.TrackNumber,
			TotalTracks: item.Album.TotalTracks,
			DiscNumber:  item.DiscNumber,
			Year:        parseYear(item.Album.ReleaseDate),
			ReleaseDate: item.Album.ReleaseDate,
			ISRC:        item.ExternalIDs.ISRC,
			ArtworkURL:  artworkURL,
			Duration:    time.Duration(item.DurationMs) * time.Millisecond,
		}
		results = append(results, info)
	}
	return results
}

func parseYear(releaseDate string) int {
	if len(releaseDate) >= 4 {
		if y, err := strconv.Atoi(releaseDate[:4]); err == nil {
			return y
		}
	}
	return 0
}

// Spotify API response types

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
}

type searchResponse struct {
	Tracks struct {
		Items []trackItem `json:"items"`
	} `json:"tracks"`
}

type trackItem struct {
	Name        string     `json:"name"`
	Artists     []artist   `json:"artists"`
	Album       albumInfo  `json:"album"`
	TrackNumber int        `json:"track_number"`
	DiscNumber  int        `json:"disc_number"`
	DurationMs  int        `json:"duration_ms"`
	ExternalIDs externalID `json:"external_ids"`
}

type artist struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type albumInfo struct {
	Name        string   `json:"name"`
	Artists     []artist `json:"artists"`
	ReleaseDate string   `json:"release_date"`
	TotalTracks int      `json:"total_tracks"`
	Images      []image  `json:"images"`
}

type image struct {
	URL    string `json:"url"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

type externalID struct {
	ISRC string `json:"isrc"`
}

type artistResponse struct {
	Genres []string `json:"genres"`
}
