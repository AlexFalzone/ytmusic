## Quick Start (Docker - Recommended)

```bash
# Start web interface
docker compose up -d ytmusic-web

# Access at http://localhost:8080
```

## Local Installation

### Prerequisites

- Go 1.23+
- yt-dlp
- FFmpeg

### Build

```bash
# CLI tool
go build -o ytmusic ./cmd/ytmusic

# Web interface
go build -o ytmusic-web ./cmd/ytmusic-web
```

## CLI Usage

```bash
# Download playlist
./ytmusic https://www.youtube.com/playlist?list=...

# Preview mode
./ytmusic --dry-run https://youtube.com/playlist?list=...

# Custom options
./ytmusic -p 8 -f flac https://youtube.com/playlist?list=...
```

### Options

```
-v, --verbose         Detailed output
-n, --dry-run         Preview only
-p, --parallel <n>    Parallel downloads (1-10, default: 4)
-b, --browser <name>  Browser for cookies
-f, --format <fmt>    Audio format (mp3, flac, m4a, opus, wav, aac)
-o, --output <dir>    Output directory (default: ~/Music)
-c, --config <path>   Config file path
    --init-config     Create config file with defaults
-h, --help            Help
```

## Web Interface

```bash
./ytmusic-web

# Custom port
./ytmusic-web -port 3000
```

## Configuration

```bash
# Initialize config
./ytmusic --init-config
```

Config locations (checked in order):
1. `./ytmusic.yaml`
2. `~/.config/ytmusic/config.yaml`
3. `~/.ytmusic.yaml`

Example `config.yaml`:
```yaml
parallel_jobs: 4
cookies_browser: brave
audio_format: mp3
output_dir: "~/Music"
verbose: false
metadata_providers:
  - spotify
  - musicbrainz
spotify_client_id: ""
spotify_client_secret: ""
confidence_threshold: 0.7
```
