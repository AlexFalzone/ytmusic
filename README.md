# ytmusic

Download YouTube Music playlists as tagged audio files, with metadata (title, artist, album, artwork, lyrics)
resolved from several providers.

## Table of Contents

- [Prerequisites](#prerequisites)
- [Build](#build)
- [Usage](#usage)
- [Configuration](#configuration)
- [Metadata Providers](#metadata-providers)
- [Web Interface](#web-interface)
- [Reverse Proxy](#reverse-proxy)
- [Docker](#docker)

## Prerequisites

- Go 1.27+
- yt-dlp
- FFmpeg (`ffmpeg` and `ffprobe`)
- Chromaprint (`fpcalc`), only when `acoustid_api_key` is set

Both binaries refuse to start if a program the run needs is missing: a download needs all of them,
`--dry-run` only yt-dlp, `--import-only` only `fpcalc` (with an AcoustID key).

## Build

```
make local   # both binaries
make test
make lint
make vuln
```

## Usage

```
ytmusic [options] <playlist_url>

-v, --verbose              Detailed output
-n, --dry-run              Preview only (no download)
-p, --parallel <n>         Parallel downloads (1-10, default: 4)
-b, --browser <name>       Browser for cookie extraction (default: none)
-f, --format <fmt>         Audio format: mp3, m4a, opus, flac, wav, aac (default: mp3)
-o, --output <dir>         Output directory (default: ~/Music)
-c, --config <path>        Config file path
    --no-lyrics            Skip lyrics fetching
    --lyrics-only <dir>    Fetch lyrics for existing audio files
    --import-only <dir>    Resolve metadata and lyrics for existing audio files (no download)
    --init-config          Create default config file
-h, --help                 Help
```

A failed video is tried a second time at the end of the downloads. Exit codes: `0` everything arrived, `1` the run
failed, `2` the run finished but some videos or files are missing, each named in the warnings on the terminal.
Missing lyrics and metadata are warnings with exit code `0`.

Nothing in the library is ever overwritten: a name already taken gives `Song_2.mp3`.

## Configuration

See `config.example.yaml`, or run `ytmusic --init-config`.

Age-restricted and private videos need the cookies of a browser you are logged in with: `-b firefox` or
`cookies_browser: firefox`. Leave it empty in Docker, where there is no browser.

## Metadata Providers

| Provider    | API Key  | Rate Limit  |
|-------------|----------|-------------|
| Spotify     | Required | Token-based |
| MusicBrainz | No       | 1 req/s     |
| Deezer      | No       | None        |
| iTunes      | No       | ~20 req/min |

AcoustID is limited to 3 requests per second. Resolution runs in three phases:

1. **Batch fingerprint** (`fpcalc` + AcoustID key): if one MusicBrainz release holds ≥ 50% of an album
   group's recordings, the files it matches take track and disc numbers, album, album artist, date and cover from it.
2. **Album-first lookup** (MusicBrainz): the album is searched once and the same is done with its tracklist,
   matched by title, one file per track.
3. **Per-file search**: providers in order, `metadata_workers` files at a time. The first result above
   `confidence_threshold` wins; later providers fill missing fields and replace artwork that fails to download.

A file placed on a release by phases 1 and 2 keeps those fields: phase 3 sets its title, artist and genre, an ISRC it
lacks and, only when the release has none, the date. An artist tag listing every featured name stays when a provider
names only the first.

**Album URLs** (`music.youtube.com/browse/MPREb_…` or `playlist?list=OLAK5uy_…`) replace phases 1 and 2: the album
keeps YouTube Music's title, order and track count (`5/19`), and every track gets the same album artist, date and
cover, recognised by a provider or not. A MusicBrainz release with the same track count and titles in the same order
adds its date, ISRCs, cover and full track titles (YouTube Music cuts them at about 100 characters); without one, the
date is the release date YouTube Music gives and the cover its thumbnail cropped square. Phase 3 then sets only artist,
genre and a missing ISRC: the title never comes from a provider.

### Matching rules

- A title declaring a variant (`(Live)`, `(Sped Up)`, `- Radio Edit`, `(Skrillex Remix)`) only matches the
  same variant. When no provider has it, the original lends artist, album, artwork, year and genre, but not
  ISRC or track number.
- A live title naming a concert (`(Live at the Forum, 1976)`) only matches the same concert: another venue or date is
  skipped, a plain `(Live)` lends what the original would but not its title or ISRC.
- A recording longer than the file by more than 10% (at least 3 s) is skipped; a file up to twice the
  recording's length is accepted (music videos add intros and outros).
- A candidate with neither a length nor an album is skipped.
- Titles are compared ignoring remaster notes and featuring credits, with accents folded and `&` read as `and`.

## Web Interface

`ytmusic-web` requires a username and a bcrypt password hash:

```bash
ytmusic-web -hash-password          # prompts, prints the hash
```

```yaml
auth:
  enabled: true
  username: "alex"
  password_hash: "$2a$12$..."
  session_ttl: "720h"
```

`auth.enabled: false` is only for an authenticating proxy in front (Authelia, oauth2-proxy).

The page lists every job with its progress, warnings and log; reloading it reattaches to jobs still running.

To restrict what can be downloaded (subdomains match on label boundaries: `youtube.com` accepts
`www.youtube.com`, not `notyoutube.com`). The list is checked against the submitted URL and each playlist entry,
not the redirects yt-dlp follows, so it does not replace network rules on the container:

```yaml
allowed_hosts:
  - youtube.com
  - music.youtube.com
```

## Reverse Proxy

Set `behind_proxy: true` only when a proxy is in front: the server then trusts `X-Forwarded-Proto` (Secure
cookie) and `X-Forwarded-For` (login throttling per client). Subpaths are not supported: use a dedicated host
or subdomain.

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

        # WebSocket for job progress
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
    }
}
```

## Docker

```bash
make build        # cli image
make build-web    # web image
```

The `make` targets pick up a new yt-dlp at most once a day, without rebuilding ffmpeg. YouTube breaks old versions
within weeks: when downloads start failing, rebuild first.

| Container path        | Host path              | Content              |
|-----------------------|------------------------|----------------------|
| `/config/config.yaml` | `./config/config.yaml` | Config, read-only    |
| `/music`              | `./music`              | Output audio files   |
| `/logs`               | `./logs`               | Log files            |
| `/tmp/.cache`         | volume `ytmusic-cache` | yt-dlp cache         |

The containers run as UID/GID 1000. Create the directories and the config first, as that user, or Docker
creates them owned by root (and `config.yaml` as a directory):

```bash
mkdir -p config music logs
cp config.example.yaml config/config.yaml
chmod 600 config/config.yaml
```

In the config set `output_dir: /music`, `log_dir: /logs` and leave `cookies_browser` empty. Then:

```bash
docker compose run --rm ytmusic-web -hash-password   # paste it into the config
make up
```
