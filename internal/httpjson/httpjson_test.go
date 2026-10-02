package httpjson

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
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
	for _, status := range []int{http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		srv, calls := serve(t,
			reply{status: status, retryAfter: "0"},
			reply{status: http.StatusOK, body: `{}`})
		c := newClient()
		c.Retry = true

		if err := c.Get(context.Background(), srv.URL, nil, &struct{}{}); err != nil {
			t.Errorf("%d: Get = %v, want the retry to succeed", status, err)
		}
		if got := calls.Load(); got != 2 {
			t.Errorf("%d: %d requests, want 2", status, got)
		}
	}
}

func TestGetRetriesOnlyOnce(t *testing.T) {
	srv, calls := serve(t, reply{status: http.StatusServiceUnavailable, retryAfter: "0"})
	c := newClient()
	c.Retry = true

	err := c.Get(context.Background(), srv.URL, nil, &struct{}{})
	var se *StatusError
	if !errors.As(err, &se) || se.Code != http.StatusServiceUnavailable {
		t.Errorf("err = %v, want the second 503", err)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("%d requests, want 2", got)
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
	srv, _ := serve(t, reply{status: http.StatusTooManyRequests, retryAfter: "3600"})
	c := newClient()
	c.Retry = true
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
