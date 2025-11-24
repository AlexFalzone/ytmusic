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

# Build binaries with aggressive optimization
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -a -installsuffix cgo -o ytmusic ./cmd/ytmusic
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -a -installsuffix cgo -o ytmusic-web ./cmd/ytmusic-web

# Strip binaries further
RUN apk add --no-cache upx && \
    upx --best --lzma ytmusic ytmusic-web

# ==================================================
# FFmpeg downloader - get static minimal build
# ==================================================
FROM alpine:3.19 AS ffmpeg-downloader

RUN apk add --no-cache curl xz && \
    curl -L https://johnvansickle.com/ffmpeg/releases/ffmpeg-release-amd64-static.tar.xz -o /tmp/ffmpeg.tar.xz && \
    mkdir /ffmpeg && \
    tar -xf /tmp/ffmpeg.tar.xz -C /ffmpeg --strip-components=1 && \
    rm /tmp/ffmpeg.tar.xz

# ==================================================
# Base runtime - minimal Python image
# ==================================================
FROM python:3.11-slim AS base

# Install only essential runtime dependencies
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        libchromaprint1 \
        ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && apt-get clean

# Copy ffmpeg static binary from downloader
COPY --from=ffmpeg-downloader /ffmpeg/ffmpeg /usr/local/bin/ffmpeg
COPY --from=ffmpeg-downloader /ffmpeg/ffprobe /usr/local/bin/ffprobe

# Install Python packages - minimal set
RUN pip install --no-cache-dir \
    yt-dlp \
    beets \
    pyacoustid \
    requests \
    pillow \
    langdetect \
    pylast \
    beautifulsoup4 \
    && pip cache purge

# Create directories
RUN mkdir -p /config /music /tmp/ytmusic /logs /root/.config/beets /root/.config/ytmusic

# Create minimal beets config (fewer plugins = less dependencies)
RUN echo "directory: /music\n\
library: /config/beets.db\n\
\n\
plugins: embedart chroma\n\
\n\
import:\n\
  move: yes\n\
  write: yes\n\
  autotag: yes\n\
  quiet_fallback: skip\n\
\n\
embedart:\n\
  auto: yes\n\
  remove_art_file: yes\n\
\n\
chroma:\n\
  auto: yes" > /root/.config/beets/config.yaml

# Create default ytmusic config
RUN echo "parallel_jobs: 4\n\
cookies_browser: brave\n\
audio_format: mp3\n\
verbose: false\n\
dry_run: false" > /root/.config/ytmusic/config.yaml

# Set environment
ENV HOME=/root \
    PATH="/usr/local/bin:${PATH}" \
    PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1

# Volumes
VOLUME ["/config", "/music", "/logs"]

# ==================================================
# CLI variant
# ==================================================
FROM base AS cli

COPY --from=builder /build/ytmusic /usr/local/bin/ytmusic
COPY config.example.yaml /etc/ytmusic/config.example.yaml

WORKDIR /tmp/ytmusic
ENTRYPOINT ["ytmusic"]
CMD ["--help"]

# ==================================================
# Web variant
# ==================================================
FROM base AS web

# Install curl (minimal version)
RUN apt-get update && \
    apt-get install -y --no-install-recommends curl && \
    rm -rf /var/lib/apt/lists/* && \
    apt-get clean

# Copy binaries
COPY --from=builder /build/ytmusic /usr/local/bin/ytmusic
COPY --from=builder /build/ytmusic-web /usr/local/bin/ytmusic-web

# Copy web static files
COPY web/static /app/web/static
COPY config.example.yaml /etc/ytmusic/config.example.yaml

WORKDIR /app
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD curl -f http://localhost:8080/api/jobs || exit 1

ENTRYPOINT ["ytmusic-web"]
CMD ["-port", "8080"]
