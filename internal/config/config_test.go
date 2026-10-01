package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestValidate(t *testing.T) {
	valid := func() Config {
		return Config{
			PlaylistURL:         "https://youtube.com/playlist?list=abc",
			ParallelJobs:        4,
			MaxConcurrentJobs:   1,
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
			modify:  func(c *Config) { c.MetadataProviders = []string{"lastfm"} },
			wantErr: true,
		},
		{
			name:   "deezer provider",
			modify: func(c *Config) { c.MetadataProviders = []string{"deezer"} },
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

func validAuthConfig() Config {
	return Config{
		ParallelJobs:        4,
		MaxConcurrentJobs:   1,
		AudioFormat:         "mp3",
		OutputDir:           "/tmp/music",
		ConfidenceThreshold: 0.7,
		Auth: AuthConfig{
			Enabled:      true,
			Username:     "alex",
			PasswordHash: "$2a$12$abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123",
			SessionTTL:   "720h",
		},
	}
}

func TestValidateAuth(t *testing.T) {
	tests := []struct {
		name       string
		modify     func(*Config)
		wantErr    bool
		errMustSay string
	}{
		{
			name:   "valid auth config",
			modify: func(c *Config) {},
		},
		{
			name:       "enabled without password hash",
			modify:     func(c *Config) { c.Auth.PasswordHash = "" },
			wantErr:    true,
			errMustSay: "-hash-password",
		},
		{
			name:    "enabled without username",
			modify:  func(c *Config) { c.Auth.Username = "" },
			wantErr: true,
		},
		{
			name:       "plaintext password instead of bcrypt hash",
			modify:     func(c *Config) { c.Auth.PasswordHash = "hunter2" },
			wantErr:    true,
			errMustSay: "bcrypt",
		},
		{
			name: "disabled without credentials",
			modify: func(c *Config) {
				c.Auth = AuthConfig{Enabled: false}
			},
		},
		{
			name:    "unparsable session ttl",
			modify:  func(c *Config) { c.Auth.SessionTTL = "banana" },
			wantErr: true,
		},
		{
			name:    "zero session ttl",
			modify:  func(c *Config) { c.Auth.SessionTTL = "0s" },
			wantErr: true,
		},
		{
			name:   "empty session ttl falls back to default",
			modify: func(c *Config) { c.Auth.SessionTTL = "" },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validAuthConfig()
			tt.modify(&cfg)

			err := cfg.ValidateWeb()
			if tt.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.errMustSay != "" && !strings.Contains(err.Error(), tt.errMustSay) {
				t.Errorf("error message must mention %q, got: %v", tt.errMustSay, err)
			}
		})
	}
}

func TestAuthTTL(t *testing.T) {
	tests := []struct {
		raw  string
		want time.Duration
	}{
		{"", defaultSessionTTL},
		{"24h", 24 * time.Hour},
		{"30m", 30 * time.Minute},
		{"nonsense", defaultSessionTTL},
	}

	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			got := AuthConfig{SessionTTL: tt.raw}.TTL()
			if got != tt.want {
				t.Errorf("TTL() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDefaultConfigEnablesAuth(t *testing.T) {
	if !DefaultConfig().Auth.Enabled {
		t.Error("auth must be enabled by default: an unauthenticated server must be a deliberate choice")
	}
}

// The CLI has no web server: auth credentials must never gate it.
func TestCLIValidateIgnoresAuth(t *testing.T) {
	cfg := DefaultConfig()
	cfg.PlaylistURL = "https://youtube.com/playlist?list=abc"
	cfg.OutputDir = "/tmp/music"

	if err := cfg.Validate(); err != nil {
		t.Fatalf("CLI validation must not require auth credentials, got: %v", err)
	}
}

// The web server must refuse to start unauthenticated by accident.
func TestValidateWebRequiresAuth(t *testing.T) {
	cfg := DefaultConfig()
	cfg.OutputDir = "/tmp/music"

	if err := cfg.ValidateWeb(); err == nil {
		t.Fatal("web validation must reject a config with auth enabled and no credentials")
	}
}

func TestValidateMaxConcurrentJobs(t *testing.T) {
	tests := []struct {
		name    string
		value   int
		wantErr bool
	}{
		{"one", 1, false},
		{"three", 3, false},
		{"zero", 0, true},
		{"negative", -1, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.OutputDir = "/tmp/music"
			cfg.MaxConcurrentJobs = tt.value

			err := cfg.ValidateBase()
			if tt.wantErr && err == nil {
				t.Error("expected an error")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestDefaultMaxConcurrentJobsIsOne(t *testing.T) {
	// One job at a time keeps yt-dlp's parallelism (parallel_jobs) as the only
	// source of concurrency against YouTube; two jobs would multiply it.
	if got := DefaultConfig().MaxConcurrentJobs; got != 1 {
		t.Errorf("MaxConcurrentJobs default = %d, want 1", got)
	}
}

func TestDefaultConfigReadsNoBrowserCookies(t *testing.T) {
	// A container has no browser to read cookies from: any default browser makes
	// every download fail there, so cookies have to be an explicit choice.
	if got := DefaultConfig().CookiesBrowser; got != "" {
		t.Errorf("CookiesBrowser default = %q, want empty", got)
	}
}

func TestDefaultLogDirIsUnderHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	want := filepath.Join(home, ".local", "share", "ytmusic", "logs")
	if got := DefaultConfig().LogDir; got != want {
		t.Errorf("LogDir default = %q, want %q", got, want)
	}
}

// In Docker the log directory has to be the mounted volume, not somewhere under
// HOME that dies with the container.
func TestLoadConfigFileReadsLogDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("log_dir: ~/ytlogs\n"), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfigFile(path)
	if err != nil {
		t.Fatalf("LoadConfigFile() error: %v", err)
	}
	if want := filepath.Join(home, "ytlogs"); cfg.LogDir != want {
		t.Errorf("LogDir = %q, want %q", cfg.LogDir, want)
	}
}
