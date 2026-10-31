package lyrics

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"ytmusic/internal/httpjson"
	"ytmusic/internal/throttle"
)

type Result struct {
	Synced string // LRC
	Plain  string
}

type Client struct {
	api    *httpjson.Client
	apiURL string
}

// LRCLib publishes no limit; with three workers and no spacing it answered 429.
const requestInterval = 500 * time.Millisecond

// Shared by every client: concurrent runs come from one address.
var limiter = throttle.New(requestInterval)

func NewClient(timeout time.Duration) *Client {
	return &Client{
		api:    &httpjson.Client{HTTP: &http.Client{Timeout: timeout}, Throttle: limiter, Retries: 2},
		apiURL: "https://lrclib.net/api/get",
	}
}

// Not found is an empty Result, not an error.
func (c *Client) Fetch(ctx context.Context, artist, title, album string) (Result, error) {
	params := url.Values{}
	params.Set("artist_name", artist)
	params.Set("track_name", title)
	params.Set("album_name", album)

	var apiResp apiResponse
	err := c.api.Get(ctx, c.apiURL+"?"+params.Encode(), nil, &apiResp)
	if se, ok := errors.AsType[*httpjson.StatusError](err); ok && se.Code == http.StatusNotFound {
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
