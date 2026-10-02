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
	Synced string // LRC
	Plain  string
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

// Not found is an empty Result, not an error.
func (c *Client) Fetch(ctx context.Context, artist, title, album string) (Result, error) {
	result, err := c.doFetch(ctx, artist, title, album)
	if err == nil {
		return result, nil
	}

	// An HTTP error would fail the same way again; a network error might not.
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
