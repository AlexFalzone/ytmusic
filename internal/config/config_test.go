package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidate(t *testing.T) {
	valid := func() Config {
		return Config{
			PlaylistURL:         "https://youtube.com/playlist?list=abc",
			ParallelJobs:        4,
			AudioFormat:         "mp3",
			OutputDir:           "/tmp/music",
			MetadataProviders:   []string{"spotify"},
			SpotifyClientID:     "id",
			SpotifyClientSecret: "secret",
			ConfidenceThreshold: 0.7,
		}
	}

	tests := []struct {
		name    string
		modify  func(*Config)
		wantErr bool
	}{
		{
			name:   "valid config",
			modify: func(c *Config) {},
		},
		{
			name:   "confidence threshold 0.0",
			modify: func(c *Config) { c.ConfidenceThreshold = 0.0 },
		},
		{
			name:   "confidence threshold 1.0",
			modify: func(c *Config) { c.ConfidenceThreshold = 1.0 },
		},
		{
			name:    "confidence threshold negative",
			modify:  func(c *Config) { c.ConfidenceThreshold = -0.1 },
			wantErr: true,
		},
		{
			name:    "confidence threshold above 1",
			modify:  func(c *Config) { c.ConfidenceThreshold = 1.1 },
			wantErr: true,
		},
		{
			name:    "parallel jobs 0",
			modify:  func(c *Config) { c.ParallelJobs = 0 },
			wantErr: true,
		},
		{
			name:    "parallel jobs 11",
			modify:  func(c *Config) { c.ParallelJobs = 11 },
			wantErr: true,
		},
		{
			name:   "parallel jobs 10",
			modify: func(c *Config) { c.ParallelJobs = 10 },
		},
		{
			name:    "invalid format",
			modify:  func(c *Config) { c.AudioFormat = "wma" },
			wantErr: true,
		},
		{
			name:    "empty URL",
			modify:  func(c *Config) { c.PlaylistURL = "" },
			wantErr: true,
		},
		{
			name:    "URL without scheme",
			modify:  func(c *Config) { c.PlaylistURL = "youtube.com/playlist" },
			wantErr: true,
		},
		{
			name:   "http URL",
			modify: func(c *Config) { c.PlaylistURL = "http://youtube.com/playlist" },
		},
		{
			name:    "empty output dir",
			modify:  func(c *Config) { c.OutputDir = "" },
			wantErr: true,
		},
		{
			name: "dry run skips URL and Spotify validation",
			modify: func(c *Config) {
				c.DryRun = true
				c.PlaylistURL = ""
				c.SpotifyClientID = ""
				c.SpotifyClientSecret = ""
			},
		},
		{
			name: "missing spotify creds with spotify provider",
			modify: func(c *Config) {
				c.MetadataProviders = []string{"spotify"}
				c.SpotifyClientID = ""
			},
			wantErr: true,
		},
		{
			name: "missing spotify secret with spotify provider",
			modify: func(c *Config) {
				c.MetadataProviders = []string{"spotify"}
				c.SpotifyClientSecret = ""
			},
			wantErr: true,
		},
		{
			name: "no spotify creds needed without spotify provider",
			modify: func(c *Config) {
				c.MetadataProviders = []string{"musicbrainz"}
				c.SpotifyClientID = ""
				c.SpotifyClientSecret = ""
			},
		},
		{
			name: "no spotify creds needed with empty providers",
			modify: func(c *Config) {
				c.MetadataProviders = nil
				c.SpotifyClientID = ""
				c.SpotifyClientSecret = ""
			},
		},
		{
			name:    "unknown provider",
			modify:  func(c *Config) { c.MetadataProviders = []string{"deezer"} },
			wantErr: true,
		},
		{
			name: "multiple valid providers",
			modify: func(c *Config) {
				c.MetadataProviders = []string{"spotify", "musicbrainz"}
			},
		},
		{
			name:   "musicbrainz only",
			modify: func(c *Config) { c.MetadataProviders = []string{"musicbrainz"} },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := valid()
			tt.modify(&cfg)
			err := cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}

func TestLoadConfigFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	content := `parallel_jobs: 8
audio_format: flac
confidence_threshold: 0.5
output_dir: /tmp/test-music
`
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfigFile(path)
	if err != nil {
		t.Fatalf("LoadConfigFile() error: %v", err)
	}

	if cfg.ParallelJobs != 8 {
		t.Errorf("ParallelJobs = %d, want 8", cfg.ParallelJobs)
	}
	if cfg.AudioFormat != "flac" {
		t.Errorf("AudioFormat = %q, want %q", cfg.AudioFormat, "flac")
	}
	if cfg.ConfidenceThreshold != 0.5 {
		t.Errorf("ConfidenceThreshold = %f, want 0.5", cfg.ConfidenceThreshold)
	}
	if cfg.OutputDir != "/tmp/test-music" {
		t.Errorf("OutputDir = %q, want %q", cfg.OutputDir, "/tmp/test-music")
	}
}

func TestLoadConfigFileNotFound(t *testing.T) {
	cfg, err := LoadConfigFile("/nonexistent/path/config.yaml")
	if err != nil {
		t.Fatalf("LoadConfigFile() should return defaults for missing file, got error: %v", err)
	}
	if cfg.ParallelJobs != 4 {
		t.Errorf("expected default ParallelJobs=4, got %d", cfg.ParallelJobs)
	}
}

func TestExpandHome(t *testing.T) {
	home := homeDir()
	tests := []struct {
		input string
		want  string
	}{
		{"~/Music", filepath.Join(home, "Music")},
		{"/absolute/path", "/absolute/path"},
		{"relative/path", "relative/path"},
		{"~notslash", "~notslash"},
	}

	for _, tt := range tests {
		got := ExpandHome(tt.input)
		if got != tt.want {
			t.Errorf("ExpandHome(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
