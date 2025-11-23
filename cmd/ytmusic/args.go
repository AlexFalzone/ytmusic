package main

import (
	"fmt"
	"os"

	"ytmusic/internal/config"
)

// parseArgs parses command-line arguments and loads configuration.
// Priority: CLI flags > config file > defaults
func parseArgs() (config.Config, string, error) {
	args := os.Args[1:]

	if len(args) == 0 {
		printUsage()
		os.Exit(1)
	}

	for _, arg := range args {
		if arg == "--help" || arg == "-h" {
			printUsage()
			os.Exit(0)
		}
		if arg == "--init-config" {
			return config.Config{}, "", initConfigFile()
		}
	}

	var configPath string
	var cfg config.Config
	var err error

	for i := 0; i < len(args); i++ {
		if args[i] == "--config" || args[i] == "-c" {
			if i+1 >= len(args) {
				return config.Config{}, "", fmt.Errorf("--config requires a path argument")
			}
			configPath = args[i+1]
			break
		}
	}

	cfg, err = config.LoadConfigFile(configPath)
	if err != nil {
		return config.Config{}, "", fmt.Errorf("failed to load config: %w", err)
	}
	if configPath == "" {
		configPath = config.FindConfigFile()
	}

	for i := 0; i < len(args); i++ {
		arg := args[i]

		switch arg {
		case "--verbose", "-v":
			cfg.Verbose = true

		case "--dry-run", "-n":
			cfg.DryRun = true

		case "--parallel", "-p":
			if i+1 >= len(args) {
				return config.Config{}, "", fmt.Errorf("--parallel requires a number argument")
			}
			i++
			var jobs int
			if _, err := fmt.Sscanf(args[i], "%d", &jobs); err != nil {
				return config.Config{}, "", fmt.Errorf("invalid parallel jobs value: %s", args[i])
			}
			cfg.ParallelJobs = jobs

		case "--browser", "-b":
			if i+1 >= len(args) {
				return config.Config{}, "", fmt.Errorf("--browser requires a browser name")
			}
			i++
			cfg.CookiesBrowser = args[i]

		case "--format", "-f":
			if i+1 >= len(args) {
				return config.Config{}, "", fmt.Errorf("--format requires a format name")
			}
			i++
			cfg.AudioFormat = args[i]

		case "--config", "-c":
			i++

		default:
			if len(arg) > 0 && arg[0] == '-' {
				return config.Config{}, "", fmt.Errorf("unknown flag: %s", arg)
			}
			cfg.PlaylistURL = arg
		}
	}

	return cfg, configPath, nil
}

// initConfigFile creates a new config file with default values
func initConfigFile() error {
	path := config.GetDefaultConfigPath()

	if _, err := os.Stat(path); err == nil {
		fmt.Printf("Config file already exists at: %s\n", path)
		fmt.Println("Delete it first if you want to recreate it.")
		os.Exit(0)
	}

	cfg := config.DefaultConfig()

	if err := config.SaveConfigFile(cfg, path); err != nil {
		return fmt.Errorf("failed to create config file: %w", err)
	}

	fmt.Printf("Created default config file at: %s\n", path)
	fmt.Println("\nYou can now edit this file to customize your settings.")
	fmt.Println("Available options:")
	fmt.Println("  parallel_jobs: 1-10 (number of parallel downloads)")
	fmt.Println("  cookies_browser: brave, chrome, firefox, etc.")
	fmt.Println("  audio_format: mp3, m4a, opus, flac, wav, aac")
	fmt.Println("  verbose: true/false (enable detailed logging)")
	fmt.Println("  dry_run: true/false (preview mode)")

	os.Exit(0)
	return nil
}

// printUsage displays the help message
func printUsage() {
	fmt.Println("ytmusic - Download YouTube playlists and import to beets")
	fmt.Println()
	fmt.Println("Usage: ytmusic [options] <playlist_url>")
	fmt.Println()
	fmt.Println("Options:")
	fmt.Println("  -v, --verbose              Show detailed output")
	fmt.Println("  -n, --dry-run              Preview what would be downloaded (no actual download)")
	fmt.Println("  -p, --parallel <n>         Number of parallel downloads (1-10, default: 4)")
	fmt.Println("  -b, --browser <name>       Browser to extract cookies from (default: brave)")
	fmt.Println("  -f, --format <format>      Audio format: mp3, m4a, opus, flac, etc. (default: mp3)")
	fmt.Println("  -c, --config <path>        Path to config file")
	fmt.Println("  -h, --help                 Show this help message")
	fmt.Println()
	fmt.Println("Configuration:")
	fmt.Println("  --init-config              Create a default config file")
	fmt.Println()
	fmt.Println("Config file locations (checked in order):")
	fmt.Println("  ./ytmusic.yaml")
	fmt.Println("  ~/.config/ytmusic/config.yaml")
	fmt.Println("  ~/.ytmusic.yaml")
	fmt.Println()
	fmt.Println("Logging:")
	fmt.Println("  Normal mode: Progress bar shown, detailed logs saved to:")
	fmt.Println("    ~/.local/share/ytmusic/logs/")
	fmt.Println("  Verbose mode: All output to stdout, no progress bar, no file logging")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  # Preview what would be downloaded")
	fmt.Println("  ytmusic --dry-run https://www.youtube.com/playlist?list=...")
	fmt.Println()
	fmt.Println("  # Download with defaults (progress bar + file logging)")
	fmt.Println("  ytmusic https://www.youtube.com/playlist?list=...")
	fmt.Println()
	fmt.Println("  # Download with verbose output (no progress bar)")
	fmt.Println("  ytmusic -v https://www.youtube.com/playlist?list=...")
	fmt.Println()
	fmt.Println("  # Download with 8 parallel jobs in FLAC format")
	fmt.Println("  ytmusic -p 8 -f flac https://www.youtube.com/playlist?list=...")
	fmt.Println()
	fmt.Println("  # Create a config file to persist settings")
	fmt.Println("  ytmusic --init-config")
}
