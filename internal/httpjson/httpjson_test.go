package httpjson

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ytmusic/internal/buildinfo"
	"ytmusic/internal/throttle"
)

func newClient() *Client {
	return &Client{HTTP: &http.Client{Timeout: 5 * time.Second}}
}

type reply struct {
	status     int
	retryAfter string
	body       string
}

// Every retry waits at least retryBackoff: seconds per test otherwise.
func shortenBackoff(t *testing.T) {
	old := retryBackoff
	retryBackoff = time.Millisecond
	t.Cleanup(func() { retryBackoff = old })
}

// The last reply repeats.
func serve(t *testing.T, replies ...reply) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(calls.Add(1)) - 1
		rep := replies[min(n, len(replies)-1)]
		if rep.retryAfter != "" {
			w.Header().Set("Retry-After", rep.retryAfter)
		}
		w.WriteHeader(rep.status)
		if _, err := w.Write([]byte(rep.body)); err != nil {
			t.Errorf("write reply: %v", err)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func TestGetSendsIdentityAndHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); got != buildinfo.UserAgent() {
			t.Errorf("User-Agent = %q, want %q", got, buildinfo.UserAgent())
		}
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Errorf("Accept = %q, want application/json", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer x" {
			t.Errorf("Authorization = %q, want the caller's header", got)
		}
		if _, err := w.Write([]byte(`{"name": "ok"}`)); err != nil {
			t.Errorf("write: %v", err)
		}
	}))
	defer srv.Close()

	var got struct{ Name string }
	err := newClient().Get(context.Background(), srv.URL, http.Header{"Authorization": {"Bearer x"}}, &got)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "ok" {
		t.Errorf("Name = %q, want ok", got.Name)
	}
}

func TestGetReportsStatusWithTheStartOfTheBody(t *testing.T) {
	srv, _ := serve(t, reply{status: http.StatusInternalServerError, body: strings.Repeat("x", 2000)})

	err := newClient().Get(context.Background(), srv.URL, nil, &struct{}{})
	var se *StatusError
	if !errors.As(err, &se) {
		t.Fatalf("err = %v, want a *StatusError", err)
	}
	if se.Code != http.StatusInternalServerError {
		t.Errorf("Code = %d, want 500", se.Code)
	}
	if len(se.Body) != maxErrorBody {
		t.Errorf("Body is %d bytes, want the first %d", len(se.Body), maxErrorBody)
	}
}

func TestGetRetriesOnceWhenAskedToSlowDown(t *testing.T) {
	shortenBackoff(t)
	for _, status := range []int{http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		srv, calls := serve(t,
			reply{status: status, retryAfter: "0"},
			reply{status: http.StatusOK, body: `{}`})
		c := newClient()
		c.Retries = 1

		if err := c.Get(context.Background(), srv.URL, nil, &struct{}{}); err != nil {
			t.Errorf("%d: Get = %v, want the retry to succeed", status, err)
		}
		if got := calls.Load(); got != 2 {
			t.Errorf("%d: %d requests, want 2", status, got)
		}
	}
}

func TestGetRetriesUpToRetries(t *testing.T) {
	shortenBackoff(t)
	srv, calls := serve(t, reply{status: http.StatusServiceUnavailable, retryAfter: "0"})
	c := newClient()
	c.Retries = 3

	err := c.Get(context.Background(), srv.URL, nil, &struct{}{})
	var se *StatusError
	if !errors.As(err, &se) || se.Code != http.StatusServiceUnavailable {
		t.Errorf("err = %v, want the last 503", err)
	}
	if got := calls.Load(); got != 4 {
		t.Errorf("%d requests, want 4", got)
	}
}

func TestGetDoesNotWaitBeyondTheCap(t *testing.T) {
	srv, calls := serve(t, reply{status: http.StatusTooManyRequests, retryAfter: "3600"})
	c := newClient()
	c.Retries = 1

	start := time.Now()
	err := c.Get(context.Background(), srv.URL, nil, &struct{}{})
	var se *StatusError
	if !errors.As(err, &se) || se.Code != http.StatusTooManyRequests {
		t.Errorf("err = %v, want the 429", err)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("%d requests, want 1", got)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("Get returned after %v, want at once", elapsed)
	}
}

func TestRetryWait(t *testing.T) {
	tests := []struct {
		retryAfter string
		attempt    int
		want       time.Duration
		ok         bool
	}{
		{"", 0, 2 * time.Second, true},
		{"", 1, 4 * time.Second, true},
		{"", 2, 8 * time.Second, true},
		{"0", 0, 2 * time.Second, true},
		{"1", 1, 4 * time.Second, true},
		{"0", 2, 8 * time.Second, true},
		{"5", 0, 5 * time.Second, true},
		{"5", 2, 8 * time.Second, true},
		{"30", 0, 30 * time.Second, true},
		{"31", 0, 0, false},
		{"Wed, 21 Oct 2015 07:28:00 GMT", 1, 4 * time.Second, true},
	}
	for _, tt := range tests {
		got, ok := retryWait(tt.retryAfter, tt.attempt)
		if ok != tt.ok || (ok && got != tt.want) {
			t.Errorf("retryWait(%q, %d) = %v, %v; want %v, %v", tt.retryAfter, tt.attempt, got, ok, tt.want, tt.ok)
		}
	}
}

func TestGetDoesNotRetryUnlessTold(t *testing.T) {
	srv, calls := serve(t, reply{status: http.StatusTooManyRequests, retryAfter: "0"})

	if err := newClient().Get(context.Background(), srv.URL, nil, &struct{}{}); err == nil {
		t.Error("Get = nil, want the 429")
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("%d requests, want 1", got)
	}
}

func TestGetStopsWaitingToRetryWhenCancelled(t *testing.T) {
	srv, _ := serve(t, reply{status: http.StatusTooManyRequests, retryAfter: "20"})
	c := newClient()
	c.Retries = 1
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)

	start := time.Now()
	err := c.Get(ctx, srv.URL, nil, &struct{}{})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("Get returned after %v, want right after the cancel", elapsed)
	}
}

func TestGetWaitsForTheThrottle(t *testing.T) {
	srv, _ := serve(t, reply{status: http.StatusOK, body: `{}`})
	c := newClient()
	c.Throttle = throttle.New(100 * time.Millisecond)

	start := time.Now()
	for range 3 {
		if err := c.Get(context.Background(), srv.URL, nil, &struct{}{}); err != nil {
			t.Fatalf("Get: %v", err)
		}
	}
	if elapsed := time.Since(start); elapsed < 200*time.Millisecond {
		t.Errorf("three requests took %v, want at least 200ms", elapsed)
	}
}

func TestGetKeepsNetworkErrorsInspectable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()

	err := newClient().Get(context.Background(), url, nil, &struct{}{})
	var netErr net.Error
	if !errors.As(err, &netErr) {
		t.Errorf("err = %v, want a net.Error in its chain", err)
	}
}

func TestGetFailsOnInvalidJSON(t *testing.T) {
	srv, _ := serve(t, reply{status: http.StatusOK, body: `not json`})
	if err := newClient().Get(context.Background(), srv.URL, nil, &struct{}{}); err == nil {
		t.Error("Get = nil, want a decoding error")
	}
}

func TestPostFormSendsTheFormInTheBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if got := r.Header.Get("Content-Type"); got != "application/x-www-form-urlencoded" {
			t.Errorf("Content-Type = %q, want a form", got)
		}
		if r.URL.RawQuery != "" {
			t.Errorf("query = %q, want the form only in the body", r.URL.RawQuery)
		}
		if got := r.PostFormValue("a"); got != "b" {
			t.Errorf("a = %q, want b", got)
		}
		if got := r.Header.Get("User-Agent"); got != buildinfo.UserAgent() {
			t.Errorf("User-Agent = %q, want %q", got, buildinfo.UserAgent())
		}
		if _, err := w.Write([]byte(`{}`)); err != nil {
			t.Errorf("write reply: %v", err)
		}
	}))
	defer srv.Close()

	if err := newClient().PostForm(context.Background(), srv.URL, nil, url.Values{"a": {"b"}}, &struct{}{}); err != nil {
		t.Fatalf("PostForm: %v", err)
	}
}

// A body reader consumed by the first attempt would send the retry empty.
func TestPostFormRetriesWithTheWholeBody(t *testing.T) {
	shortenBackoff(t)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.PostFormValue("a"); got != "b" {
			t.Errorf("request %d: a = %q, want b", calls.Load()+1, got)
		}
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if _, err := w.Write([]byte(`{}`)); err != nil {
			t.Errorf("write reply: %v", err)
		}
	}))
	defer srv.Close()
	c := newClient()
	c.Retries = 1

	if err := c.PostForm(context.Background(), srv.URL, nil, url.Values{"a": {"b"}}, &struct{}{}); err != nil {
		t.Fatalf("PostForm: %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("%d requests, want 2", got)
	}
}
