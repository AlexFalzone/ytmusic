package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config contains the program configuration
type Config struct {
	PlaylistURL         string   `yaml:"playlist_url"`
	Verbose             bool     `yaml:"verbose"`
	DryRun              bool     `yaml:"dry_run"`
	ParallelJobs        int      `yaml:"parallel_jobs"`
	CookiesBrowser      string   `yaml:"cookies_browser"`
	AudioFormat         string   `yaml:"audio_format"`
	MetadataProviders   []string `yaml:"metadata_providers"`
	SpotifyClientID     string   `yaml:"spotify_client_id"`
	SpotifyClientSecret string   `yaml:"spotify_client_secret"`
	ConfidenceThreshold float64  `yaml:"confidence_threshold"`
	OutputDir           string   `yaml:"output_dir"`
}

// DefaultConfig returns the default configuration
func DefaultConfig() Config {
	return Config{
		Verbose:        false,
		DryRun:         false,
		ParallelJobs:   4,
		CookiesBrowser: "brave",
		AudioFormat:    "mp3",
		OutputDir:      filepath.Join(homeDir(), "Music"),
	}
}

// LoadConfigFile loads configuration from a YAML file.
// If path is empty, searches standard locations. Returns defaults if no file found.
func LoadConfigFile(path string) (Config, error) {
	cfg := DefaultConfig()

	if path == "" {
		path = FindConfigFile()
		if path == "" {
			return cfg, nil
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, fmt.Errorf("failed to read config file %s: %w", path, err)
	}

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("failed to parse config file %s: %w", path, err)
	}

	cfg.OutputDir = ExpandHome(cfg.OutputDir)

	return cfg, nil
}

// ExpandHome replaces a leading ~ with the user's home directory.
func ExpandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(homeDir(), path[2:])
	}
	return path
}

// FindConfigFile searches for a config file in standard locations
func FindConfigFile() string {
	home := homeDir()
	locations := []string{
		"./ytmusic.yaml",
		"./ytmusic.yml",
		filepath.Join(home, ".config", "ytmusic", "config.yaml"),
		filepath.Join(home, ".config", "ytmusic", "config.yml"),
		filepath.Join(home, ".ytmusic.yaml"),
		filepath.Join(home, ".ytmusic.yml"),
	}

	for _, path := range locations {
		if path == "" {
			continue
		}
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}

	return ""
}

// SaveConfigFile saves the current configuration to a YAML file
func SaveConfigFile(cfg Config, path string) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}

// GetDefaultConfigPath returns the default config file path
func GetDefaultConfigPath() string {
	return filepath.Join(homeDir(), ".config", "ytmusic", "config.yaml")
}

// GetDefaultLogPath returns the default log directory path
func GetDefaultLogPath() string {
	return filepath.Join(homeDir(), ".local", "share", "ytmusic", "logs")
}

func homeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return os.Getenv("HOME")
	}
	return home
}

// Validate checks if the configuration is valid
func (c *Config) Validate() error {
	// DryRun mode doesn't require URL validation
	if c.DryRun && c.PlaylistURL == "" {
		return nil
	}

	if c.PlaylistURL == "" {
		return fmt.Errorf("playlist URL cannot be empty")
	}
	if !strings.HasPrefix(c.PlaylistURL, "http://") && !strings.HasPrefix(c.PlaylistURL, "https://") {
		return fmt.Errorf("playlist URL must start with http:// or https://")
	}

	if c.ParallelJobs < 1 {
		return fmt.Errorf("parallel jobs must be at least 1, got %d", c.ParallelJobs)
	}
	if c.ParallelJobs > 10 {
		return fmt.Errorf("parallel jobs cannot exceed 10 (to avoid rate limiting), got %d", c.ParallelJobs)
	}

	validFormats := []string{"mp3", "m4a", "opus", "flac", "wav", "aac"}
	isValid := false
	for _, format := range validFormats {
		if c.AudioFormat == format {
			isValid = true
			break
		}
	}
	if !isValid {
		return fmt.Errorf("unsupported audio format '%s', valid formats: %v", c.AudioFormat, validFormats)
	}

	if c.OutputDir == "" {
		return fmt.Errorf("output_dir cannot be empty")
	}

	if c.ConfidenceThreshold < 0 || c.ConfidenceThreshold > 1 {
		return fmt.Errorf("confidence_threshold must be between 0.0 and 1.0, got %.2f", c.ConfidenceThreshold)
	}

	validProviders := map[string]bool{"spotify": true, "musicbrainz": true}
	for _, p := range c.MetadataProviders {
		if !validProviders[p] {
			return fmt.Errorf("unknown metadata provider %q, valid providers: spotify, musicbrainz", p)
		}
	}

	if !c.DryRun && c.hasProvider("spotify") {
		if c.SpotifyClientID == "" {
			return fmt.Errorf("spotify_client_id is required when spotify is in metadata_providers")
		}
		if c.SpotifyClientSecret == "" {
			return fmt.Errorf("spotify_client_secret is required when spotify is in metadata_providers")
		}
	}

	return nil
}

func (c *Config) hasProvider(name string) bool {
	for _, p := range c.MetadataProviders {
		if p == name {
			return true
		}
	}
	return false
}
