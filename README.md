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
- [Web Interface](#web-interface)
- [Reverse Proxy](#reverse-proxy)
- [Docker](#docker)

## Prerequisites

- Go 1.27+
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

### Matching rules

Before any candidate is scored, it has to pass two checks:

- **Version.** A title that declares a variant — `(Live)`, `(Sped Up)`, `- Radio Edit`, `(Skrillex Remix)` —
  only matches the same variant, and a plain title never matches a variant. When no provider carries the
  variant, the original recording lends its artist, album, artwork, year and genre, and the title keeps the variant:
  `Blinding Lights (Sped Up)`. Its ISRC and track number are not copied: they identify the original.
- **Length.** A recording that runs longer than the file by more than 10% (or by more than 3 seconds, for
  short tracks) is a different cut and is skipped. A longer file is accepted up to twice the recording's length, since music
  videos often wrap the song in an intro and an outro.

When comparing titles, remaster notes, featuring credits, accents and `&`/`and` are ignored on both sides.

## Web Interface

`ytmusic-web` serves the browser UI and requires a username and password.

> **Upgrading?** Authentication is on by default, so a `config.yaml` written before this change has no
> `auth` section and the server will refuse to start. That is deliberate: the web server used to be open
> to anyone who could reach the port. Follow the two steps below to get running again.

**1. Generate a password hash**

```bash
ytmusic-web -hash-password
```

It prompts for the password (twice, without echoing it) and prints a bcrypt hash. Only the hash is stored;
the password is never written anywhere. Do not pass the password as an argument — it would land in your
shell history and be visible in `ps`.

**2. Put it in your config**

```yaml
auth:
  enabled: true
  username: "alex"
  password_hash: "$2a$12$..."
  session_ttl: "720h"
```

In Docker, generate the hash inside the container:

```bash
docker compose run --rm ytmusic-web -hash-password
```

**Turning authentication off**

`auth.enabled: false` is supported for one case: something else in front already authenticates, such as
Authelia or oauth2-proxy. The server prints a warning at every startup. Anyone who can reach the port can
control it, so never do this on an instance reachable beyond localhost.

**Restricting what can be downloaded**

yt-dlp supports well over a thousand sites. To keep the instance to the ones you actually use:

```yaml
allowed_hosts:
  - youtube.com
  - music.youtube.com
```

Subdomains are matched on label boundaries: `youtube.com` accepts `www.youtube.com` but not
`notyoutube.com`. Leave the list out for no restriction.

## Reverse Proxy

Set `behind_proxy: true` **only** when a proxy really is in front. It makes the server trust
`X-Forwarded-Proto` (so the session cookie gets its `Secure` flag over HTTPS) and `X-Forwarded-For` (so
failed logins are throttled per real client IP instead of all appearing to come from the proxy). Trusting
those headers with no proxy in front would let any client forge them.

**Subpaths are not supported.** Use a dedicated host or subdomain: the frontend requests its assets from
absolute paths, so a `location /ytmusic/` mapping serves a blank page with no visible error.

Caddy:

```
music.example.com {
    reverse_proxy localhost:8080
}
```

nginx:

```nginx
server {
    server_name music.example.com;

    location / {
        proxy_pass http://localhost:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;

        # The job progress stream needs an upgrade to WebSocket.
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
    }
}
```

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
