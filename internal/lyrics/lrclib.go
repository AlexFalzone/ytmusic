package lyrics

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	"ytmusic/internal/httpjson"
)

type Result struct {
	Synced string // LRC format with timestamps, empty if unavailable
	Plain  string // plain text lyrics, empty if unavailable
}

type Client struct {
	api    *httpjson.Client
	apiURL string
}

func NewClient() *Client {
	return &Client{
		api:    &httpjson.Client{HTTP: &http.Client{Timeout: 10 * time.Second}},
		apiURL: "https://lrclib.net/api/get",
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

	var apiResp apiResponse
	err := c.api.Get(ctx, c.apiURL+"?"+params.Encode(), nil, &apiResp)
	var se *httpjson.StatusError
	if errors.As(err, &se) && se.Code == http.StatusNotFound {
		return Result{}, nil
	}
	if err != nil {
		return Result{}, fmt.Errorf("lrclib: %w", err)
	}
	return Result{Synced: apiResp.SyncedLyrics, Plain: apiResp.PlainLyrics}, nil
}

type apiResponse struct {
	SyncedLyrics string `json:"syncedLyrics"`
	PlainLyrics  string `json:"plainLyrics"`
}
