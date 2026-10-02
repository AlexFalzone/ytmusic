package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"ytmusic/internal/config"
	"ytmusic/internal/logger"
)

func TestRunRemovesItsTempDir(t *testing.T) {
	bin := t.TempDir()
	ytdlp := "#!/bin/sh\necho https://www.youtube.com/watch?v=x\n"
	if err := os.WriteFile(filepath.Join(bin, "yt-dlp"), []byte(ytdlp), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)

	cfg := config.DefaultConfig()
	cfg.DryRun = true
	cfg.PlaylistURL = "https://www.youtube.com/playlist?list=x"
	if err := run(context.Background(), cfg, logger.New(false)); err != nil {
		t.Fatalf("run: %v", err)
	}

	left, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range left {
		t.Errorf("left behind in the temp folder: %s", e.Name())
	}
}
