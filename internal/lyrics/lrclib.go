package lyrics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"
)

type Result struct {
	Synced string // LRC format with timestamps, empty if unavailable
	Plain  string // plain text lyrics, empty if unavailable
}

type Client struct {
	httpClient *http.Client
	apiURL     string
}

func NewClient() *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 10 * time.Second},
		apiURL:     "https://lrclib.net/api/get",
	}
}

// Fetch retrieves lyrics for the given track from LRCLib.
// Returns empty Result (no error) when lyrics are not found.
// Retries once on transient network errors.
func (c *Client) Fetch(ctx context.Context, artist, title, album string) (Result, error) {
	result, err := c.doFetch(ctx, artist, title, album)
	if err == nil {
		return result, nil
	}

	// Only retry on network-level errors (timeout, connection reset, etc.)
	// Don't retry on API errors (4xx, 5xx) which would fail identically.
	if !isTransient(err) {
		return Result{}, err
	}

	select {
	case <-ctx.Done():
		return Result{}, err
	case <-time.After(2 * time.Second):
	}
	return c.doFetch(ctx, artist, title, album)
}

func isTransient(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr)
}

func (c *Client) doFetch(ctx context.Context, artist, title, album string) (Result, error) {
	params := url.Values{}
	params.Set("artist_name", artist)
	params.Set("track_name", title)
	params.Set("album_name", album)

	reqURL := fmt.Sprintf("%s?%s", c.apiURL, params.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return Result{}, fmt.Errorf("failed to create lrclib request: %w", err)
	}
	req.Header.Set("User-Agent", "ytmusic/1.0")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("lrclib request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return Result{}, nil
	}
	if resp.StatusCode != http.StatusOK {
		return Result{}, fmt.Errorf("lrclib returned status %d", resp.StatusCode)
	}

	var apiResp apiResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return Result{}, fmt.Errorf("failed to decode lrclib response: %w", err)
	}

	return Result{
		Synced: apiResp.SyncedLyrics,
		Plain:  apiResp.PlainLyrics,
	}, nil
}

type apiResponse struct {
	SyncedLyrics string `json:"syncedLyrics"`
	PlainLyrics  string `json:"plainLyrics"`
}
