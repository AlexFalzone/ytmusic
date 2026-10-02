package httpjson

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"strconv"
	"strings"
	"time"

	"ytmusic/internal/buildinfo"
	"ytmusic/internal/throttle"
)

// Enough for an API's error text, not a whole HTML error page.
const maxErrorBody = 512

const defaultRetryAfter = 2 * time.Second

type Client struct {
	HTTP     *http.Client
	Throttle *throttle.Throttle
	Retry    bool // once, on 429 and 503
}

type StatusError struct {
	Code int
	Body string // truncated to maxErrorBody
}

func (e *StatusError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("HTTP %d", e.Code)
	}
	return fmt.Sprintf("HTTP %d: %s", e.Code, e.Body)
}

// Any status other than 200 is a *StatusError.
func (c *Client) Get(ctx context.Context, url string, header http.Header, dest any) error {
	resp, err := c.do(ctx, url, header)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// The body only adds detail: the status alone still says what went wrong.
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		return &StatusError{Code: resp.StatusCode, Body: strings.TrimSpace(string(body))}
	}
	if err := json.NewDecoder(resp.Body).Decode(dest); err != nil {
		return fmt.Errorf("decoding response: %w", err)
	}
	return nil
}

func (c *Client) do(ctx context.Context, url string, header http.Header) (*http.Response, error) {
	resp, err := c.send(ctx, url, header)
	if err != nil || !c.Retry || !slowDown(resp.StatusCode) {
		return resp, err
	}
	wait := retryAfter(resp.Header.Get("Retry-After"))
	resp.Body.Close()

	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("waiting to retry: %w", ctx.Err())
	case <-timer.C:
	}
	return c.send(ctx, url, header)
}

func (c *Client) send(ctx context.Context, url string, header http.Header) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	maps.Copy(req.Header, header)
	req.Header.Set("User-Agent", buildinfo.UserAgent())

	if c.Throttle != nil {
		if err := c.Throttle.Wait(ctx); err != nil {
			return nil, fmt.Errorf("waiting for the rate limit: %w", err)
		}
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sending request: %w", err)
	}
	return resp, nil
}

func slowDown(status int) bool {
	return status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable
}

// The HTTP-date form of Retry-After waits the default.
func retryAfter(v string) time.Duration {
	if s, err := strconv.Atoi(v); err == nil && s >= 0 {
		return time.Duration(s) * time.Second
	}
	return defaultRetryAfter
}
