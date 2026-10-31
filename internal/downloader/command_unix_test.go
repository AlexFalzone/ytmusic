//go:build unix

package downloader

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"ytmusic/internal/config"
	"ytmusic/internal/logger"
)

func TestCancelKillsYtdlpsChildren(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child")
	fakeYtdlp(t, fmt.Sprintf(`sleep 60 & echo $! > %q; wait`, pidFile), 0)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	d := New(config.DefaultConfig(), logger.New(false), t.TempDir())

	done := make(chan error, 1)
	go func() {
		_, err := d.DownloadSingle(ctx, "https://example.com/watch?v=x")
		done <- err
	}()

	var pid int
	deadline := time.Now().Add(10 * time.Second)
	for pid == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the fake yt-dlp never started its child")
		}
		if raw, err := os.ReadFile(pidFile); err == nil {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(raw))) // not yet written: try again
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want the cancellation", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("DownloadSingle did not return after the cancel")
	}

	deadline = time.Now().Add(5 * time.Second)
	for syscall.Kill(pid, 0) == nil {
		if time.Now().After(deadline) {
			t.Fatalf("child %d outlived the cancel", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
