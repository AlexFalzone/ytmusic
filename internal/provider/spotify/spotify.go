package spotify

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode"

	"ytmusic/internal/buildinfo"
	"ytmusic/internal/httpjson"
	"ytmusic/internal/memo"
	"ytmusic/internal/metadata"
)

type Client struct {
	clientID     string
	clientSecret string
	api          *httpjson.Client

	mu          sync.Mutex
	accessToken string
	tokenExpiry time.Time

	genres memo.Cache[string, []string] // by artist ID

	tokenURL string
	apiURL   string
}

func New(clientID, clientSecret string) *Client {
	return &Client{
		clientID:     clientID,
		clientSecret: clientSecret,
		api:          &httpjson.Client{HTTP: &http.Client{Timeout: 10 * time.Second}, Retry: true},
		tokenURL:     "https://accounts.spotify.com/api/token",
		apiURL:       "https://api.spotify.com/v1",
	}
}

func (c *Client) Name() string { return "spotify" }

func (c *Client) Search(ctx context.Context, query metadata.SearchQuery) ([]metadata.TrackInfo, error) {
	q := buildSearchQuery(query)
	if q == "" {
		return nil, nil
	}

	token, err := c.getToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("spotify auth failed: %w", err)
	}

	var searchResp searchResponse
	reqURL := fmt.Sprintf("%s/search?type=track&limit=5&q=%s", c.apiURL, url.QueryEscape(q))
	if err := c.api.Get(ctx, reqURL, bearer(token), &searchResp); err != nil {
		return nil, fmt.Errorf("spotify search: %w", err)
	}

	results := parseSearchResults(searchResp)
	c.enrichGenres(ctx, results, searchResp)
	return results, nil
}

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

func (c *Client) getArtistGenres(ctx context.Context, artistID string) ([]string, error) {
	return c.genres.Do(artistID, func() ([]string, error) {
		token, err := c.getToken(ctx)
		if err != nil {
			return nil, err
		}

		var artistResp artistResponse
		reqURL := fmt.Sprintf("%s/artists/%s", c.apiURL, url.PathEscape(artistID))
		if err := c.api.Get(ctx, reqURL, bearer(token), &artistResp); err != nil {
			return nil, fmt.Errorf("spotify artist %s: %w", artistID, err)
		}
		return artistResp.Genres, nil
	})
}

func formatGenres(genres []string) string {
	formatted := make([]string, min(3, len(genres)))
	for i := range formatted {
		formatted[i] = titleCase(genres[i])
	}
	return strings.Join(formatted, ", ")
}

func titleCase(s string) string {
	words := strings.Fields(s)
	for i, w := range words {
		r := []rune(w)
		if len(r) > 0 {
			r[0] = unicode.ToUpper(r[0])
			words[i] = string(r)
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
	req.Header.Set("User-Agent", buildinfo.UserAgent())
	req.SetBasicAuth(c.clientID, c.clientSecret)

	resp, err := c.api.HTTP.Do(req)
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
	c.tokenExpiry = time.Now().Add(time.Duration(tokenResp.ExpiresIn-60) * time.Second)

	return c.accessToken, nil
}

func bearer(token string) http.Header {
	return http.Header{"Authorization": {"Bearer " + token}}
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
			Year:        metadata.ParseYear(item.Album.ReleaseDate),
			ReleaseDate: item.Album.ReleaseDate,
			ISRC:        item.ExternalIDs.ISRC,
			ArtworkURL:  artworkURL,
			Duration:    time.Duration(item.DurationMs) * time.Millisecond,
		}
		results = append(results, info)
	}
	return results
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
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
	URL string `json:"url"`
}

type externalID struct {
	ISRC string `json:"isrc"`
}

type artistResponse struct {
	Genres []string `json:"genres"`
}
