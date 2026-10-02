// Package httpjson sends GET requests to the JSON APIs the program talks to:
// the metadata providers, AcoustID, LRCLib. Every request identifies the
// program, waits for the service's throttle when it has one, and may retry
// once when the service asks to slow down.
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

// maxErrorBody is how much of an error answer goes into the message: enough
// for an API's own error text, not a whole HTML error page.
const maxErrorBody = 512

// defaultRetryAfter is the wait before a retry when the answer names none.
const defaultRetryAfter = 2 * time.Second

// Client sends GET requests to one JSON API and decodes the answers.
type Client struct {
	HTTP     *http.Client
	Throttle *throttle.Throttle // nil: requests are not spaced
	Retry    bool               // one retry on 429 and 503, after Retry-After
}

// StatusError is an answer other than 200 OK.
type StatusError struct {
	Code int
	Body string // the start of the body
}

func (e *StatusError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("HTTP %d", e.Code)
	}
	return fmt.Sprintf("HTTP %d: %s", e.Code, e.Body)
}

// Get sends a GET to url with the given extra headers and decodes the JSON
// body into dest. Any status other than 200 is a *StatusError.
func (c *Client) Get(ctx context.Context, url string, header http.Header, dest any) error {
	resp, err := c.do(ctx, url, header)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// The body only adds detail to the error: if it cannot be read, the
		// status alone still says what went wrong.
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		return &StatusError{Code: resp.StatusCode, Body: strings.TrimSpace(string(body))}
	}
	if err := json.NewDecoder(resp.Body).Decode(dest); err != nil {
		return fmt.Errorf("decoding response: %w", err)
	}
	return nil
}

// do sends the request, and when the client retries and the service answers
// 429 or 503, sends it once more after the wait the service asks for.
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

// retryAfter reads a Retry-After given in seconds; anything else, the
// HTTP-date form included, waits the default.
func retryAfter(v string) time.Duration {
	if s, err := strconv.Atoi(v); err == nil && s >= 0 {
		return time.Duration(s) * time.Second
	}
	return defaultRetryAfter
}
