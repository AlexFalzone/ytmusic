package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// isolate keeps parseArgs from picking up a real config file from the
// developer's home directory.
func isolate(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
}

func TestParseArgsParallel(t *testing.T) {
	tests := []struct {
		value   string
		want    int
		wantErr bool
	}{
		{value: "4", want: 4},
		{value: "4abc", wantErr: true},
		{value: "4.5", wantErr: true},
		{value: " 4", wantErr: true},
		{value: "abc", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			isolate(t)
			cfg, _, err := parseArgs([]string{"--parallel", tt.value, "https://example.com/p"})
			if tt.wantErr {
				if err == nil {
					t.Fatalf("want an error for %q, got parallel jobs %d", tt.value, cfg.ParallelJobs)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg.ParallelJobs != tt.want {
				t.Errorf("ParallelJobs = %d, want %d", cfg.ParallelJobs, tt.want)
			}
		})
	}
}

func TestParseArgsRejectsSecondPlaylistURL(t *testing.T) {
	isolate(t)
	_, _, err := parseArgs([]string{"https://example.com/a", "https://example.com/b"})
	if err == nil {
		t.Fatal("want an error when two playlist URLs are given, got none")
	}
	if !strings.Contains(err.Error(), "https://example.com/b") {
		t.Errorf("error should name the extra argument, got: %v", err)
	}
}

func TestParseArgsKeepsSinglePlaylistURL(t *testing.T) {
	isolate(t)
	cfg, _, err := parseArgs([]string{"-v", "https://example.com/a"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.PlaylistURL != "https://example.com/a" {
		t.Errorf("PlaylistURL = %q, want %q", cfg.PlaylistURL, "https://example.com/a")
	}
}

func TestParseArgsURLOverridesConfigFile(t *testing.T) {
	isolate(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("playlist_url: https://example.com/from-config\n"), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, _, err := parseArgs([]string{"--config", path, "https://example.com/from-cli"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.PlaylistURL != "https://example.com/from-cli" {
		t.Errorf("PlaylistURL = %q, want the one from the command line", cfg.PlaylistURL)
	}
}
