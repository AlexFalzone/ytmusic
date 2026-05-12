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

Metadata resolution runs in three phases:

1. **Batch fingerprint** (requires `fpcalc` + AcoustID API key): all files in an album group are fingerprinted in parallel. If a single MusicBrainz release accounts for ≥ 50% of the matched recordings, its tracklist is used to assign track and disc numbers.
2. **Album-first lookup** (MusicBrainz): for files not resolved by phase 1, the album name is searched once and the full tracklist is matched by title similarity.
3. **Per-file text search**: each file is searched individually across all configured providers in order. The first result above the confidence threshold wins; remaining providers fill missing fields (genre, artwork, ISRC, etc.).

Track and disc numbers written by phases 1 and 2 are never overwritten by phase 3.

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
