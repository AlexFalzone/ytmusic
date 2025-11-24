# ==================================================
# Builder stage - compile Go binaries
# ==================================================
FROM golang:1.21-alpine AS builder

WORKDIR /build

# Install build dependencies
RUN apk add --no-cache git

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build binaries
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o ytmusic ./cmd/ytmusic
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o ytmusic-web ./cmd/ytmusic-web

# ==================================================
# Base runtime - common setup for both variants
# ==================================================
FROM python:3.11-slim AS base

# Install system dependencies (common to both)
RUN apt-get update && apt-get install -y \
    ffmpeg \
    libchromaprint-tools \
    && rm -rf /var/lib/apt/lists/*

# Install Python packages with all plugin dependencies
RUN pip install --no-cache-dir \
    yt-dlp \
    beets \
    pyacoustid \
    requests \
    pillow \
    langdetect \
    pylast \
    bs4

# Create directories
RUN mkdir -p /config /music /tmp/ytmusic /logs /root/.config/beets /root/.config/ytmusic

# Create comprehensive beets config with all plugins
RUN echo "directory: /music\n\
library: /config/beets.db\n\
\n\
plugins: lyrics fetchart embedart lastgenre replaygain chroma duplicates missing\n\
\n\
import:\n\
  move: yes\n\
  write: yes\n\
  autotag: yes\n\
  quiet_fallback: skip\n\
\n\
lyrics:\n\
  auto: yes\n\
  fallback: ''\n\
  force: no\n\
\n\
fetchart:\n\
  auto: yes\n\
  sources: coverart itunes amazon albumart\n\
\n\
embedart:\n\
  auto: yes\n\
  remove_art_file: yes\n\
\n\
lastgenre:\n\
  auto: yes\n\
  force: no\n\
\n\
replaygain:\n\
  auto: yes\n\
  backend: ffmpeg\n\
  overwrite: no\n\
  albumgain: yes\n\
\n\
chroma:\n\
  auto: yes" > /root/.config/beets/config.yaml

# Create default ytmusic config
RUN echo "parallel_jobs: 4\n\
cookies_browser: brave\n\
audio_format: mp3\n\
verbose: false\n\
dry_run: false" > /root/.config/ytmusic/config.yaml

# Set common environment variables
ENV HOME=/root
ENV PATH="/usr/local/bin:${PATH}"

# Volumes for persistence
VOLUME ["/config", "/music", "/logs"]

# ==================================================
# CLI variant - for command-line usage
# ==================================================
FROM base AS cli

# Copy CLI binary from builder
COPY --from=builder /build/ytmusic /usr/local/bin/ytmusic

# Copy example config
COPY config.example.yaml /etc/ytmusic/config.example.yaml

# Working directory
WORKDIR /tmp/ytmusic

# Default command
ENTRYPOINT ["ytmusic"]
CMD ["--help"]

# ==================================================
# Web variant - for web interface
# ==================================================
FROM base AS web

# Install curl for healthcheck
RUN apt-get update && apt-get install -y curl && rm -rf /var/lib/apt/lists/*

# Copy both binaries from builder
COPY --from=builder /build/ytmusic /usr/local/bin/ytmusic
COPY --from=builder /build/ytmusic-web /usr/local/bin/ytmusic-web

# Copy web static files
COPY web/static /app/web/static

# Copy example config
COPY config.example.yaml /etc/ytmusic/config.example.yaml

# Working directory
WORKDIR /app

# Expose web port
EXPOSE 8080

# Health check
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD curl -f http://localhost:8080/api/jobs || exit 1

# Default command
ENTRYPOINT ["ytmusic-web"]
CMD ["-port", "8080"]
