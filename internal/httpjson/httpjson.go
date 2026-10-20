package httpjson

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"ytmusic/internal/buildinfo"
	"ytmusic/internal/throttle"
)

// Enough for an API's error text, not a whole HTML error page.
const maxErrorBody = 512

// Doubled on each attempt. A var so tests can shorten it.
var retryBackoff = 2 * time.Second

// A service asking for a longer wait is down for longer than a run should hang on it.
const maxRetryAfter = 30 * time.Second

type Client struct {
	HTTP     *http.Client
	Throttle *throttle.Throttle
	Retries  int // on 429 and 503
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

// Built anew for each attempt: a retried POST needs its body again.
type newRequest func(ctx context.Context) (*http.Request, error)

// Any status other than 200 is a *StatusError.
func (c *Client) Get(ctx context.Context, url string, header http.Header, dest any) error {
	return c.fetch(ctx, header, dest, func(ctx context.Context) (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	})
}

// Like Get, with form in the body: unlike a query string, it stays out of the URL every error quotes.
func (c *Client) PostForm(ctx context.Context, url string, header http.Header, form url.Values, dest any) error {
	body := form.Encode()
	return c.fetch(ctx, header, dest, func(ctx context.Context) (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return req, nil
	})
}

func (c *Client) fetch(ctx context.Context, header http.Header, dest any, build newRequest) error {
	resp, err := c.do(ctx, header, build)
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

func (c *Client) do(ctx context.Context, header http.Header, build newRequest) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		resp, err := c.send(ctx, header, build)
		if err != nil || attempt == c.Retries || !slowDown(resp.StatusCode) {
			return resp, err
		}
		wait, ok := retryWait(resp.Header.Get("Retry-After"), attempt)
		if !ok {
			return resp, nil
		}
		resp.Body.Close()

		if err := sleep(ctx, wait); err != nil {
			return nil, fmt.Errorf("waiting to retry: %w", err)
		}
	}
}

func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (c *Client) send(ctx context.Context, header http.Header, build newRequest) (*http.Response, error) {
	req, err := build(ctx)
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

// Retry-After in seconds is a floor under the backoff, not the wait: MusicBrainz sheds load with Retry-After: 0, and an immediate retry meets the same overload.
// The HTTP-date form counts as absent. false: the service asks for more than maxRetryAfter.
func retryWait(retryAfter string, attempt int) (time.Duration, bool) {
	backoff := retryBackoff << attempt
	s, err := strconv.Atoi(retryAfter)
	if err != nil || s < 0 {
		return backoff, true
	}
	asked := time.Duration(s) * time.Second
	return max(asked, backoff), asked <= maxRetryAfter
}
