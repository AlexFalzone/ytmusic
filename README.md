# ytmusic

Download YouTube Music as tagged audio files. Automatically resolves metadata (title, artist, album, artwork, lyrics)
from multiple providers.

## Table of Contents

- [Prerequisites](#prerequisites)
- [Build](#build)
- [Usage](#usage)
- [Options](#options)
- [Configuration](#configuration)
- [Metadata Providers](#metadata-providers)
- [Docker](#docker)

## Prerequisites

- Go 1.23+
- yt-dlp
- FFmpeg

## Build

```
make local   // Build both CLI and web
make test    // Run tests
```

## Usage

```
ytmusic [options] <playlist_url>
```

## Options

```
-v, --verbose              Detailed output
-n, --dry-run              Preview only (no download)
-p, --parallel <n>         Parallel downloads (1-10, default: 4)
-b, --browser <name>       Browser for cookie extraction (default: brave)
-f, --format <fmt>         Audio format: mp3, m4a, opus, flac, wav, aac (default: mp3)
-o, --output <dir>         Output directory (default: ~/Music)
-c, --config <path>        Config file path
    --no-lyrics            Skip lyrics fetching
    --lyrics-only <dir>    Fetch lyrics for existing audio files
    --import-only <dir>    Resolve metadata for existing audio files (no download)
    --init-config          Create default config file
-h, --help                 Help
```

## Configuration

Look at `config.example.yaml` or just run `./ytmusic --init-config`

## Metadata Providers

| Provider    | API Key  | Rate Limit  |
|-------------|----------|-------------|
| Spotify     | Required | Token-based |
| MusicBrainz | No       | 1 req/s     |
| Deezer      | No       | None        |
| iTunes      | No       | None        |

Providers are tried in order. The first match above the confidence threshold wins. Missing fields (genre, track number,
artwork, etc.) are filled by subsequent providers.

## Docker

Uses a multi-stage Dockerfile (Go builder + python-slim runtime with yt-dlp and FFmpeg static).

```bash
make build                         # Build cli image
make build-web                     # Build web image
```

Volumes mounted from `docker-compose.yml`:

| Container path | Host path | Content |
|---------------|-----------|---------|
| `/config`     | `./config`| Config YAML |
| `/music`      | `./music` | Output audio files |
| `/logs`       | `./logs`  | Log files |
