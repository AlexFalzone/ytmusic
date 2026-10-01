# ==================================================
# Builder stage - compile Go binaries
# ==================================================
FROM golang:1.27-alpine AS builder

WORKDIR /build

RUN apk add --no-cache git

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# .git is not in the build context, so the version comes from the caller:
# docker compose passes the VERSION the Makefile exports (git describe).
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w -X ytmusic/internal/buildinfo.Version=${VERSION}" -a -installsuffix cgo -o ytmusic ./cmd/ytmusic
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w -X ytmusic/internal/buildinfo.Version=${VERSION}" -a -installsuffix cgo -o ytmusic-web ./cmd/ytmusic-web

RUN apk add --no-cache upx && \
    upx --best --lzma ytmusic ytmusic-web

# ==================================================
# FFmpeg downloader - get static minimal build
# ==================================================
FROM alpine:3.19 AS ffmpeg-downloader

RUN apk add --no-cache curl xz && \
    curl -fL https://johnvansickle.com/ffmpeg/releases/ffmpeg-release-amd64-static.tar.xz -o /tmp/ffmpeg-release-amd64-static.tar.xz && \
    curl -fL https://johnvansickle.com/ffmpeg/releases/ffmpeg-release-amd64-static.tar.xz.md5 -o /tmp/ffmpeg-release-amd64-static.tar.xz.md5 && \
    cd /tmp && md5sum -c ffmpeg-release-amd64-static.tar.xz.md5 && \
    mkdir /ffmpeg && \
    tar -xf /tmp/ffmpeg-release-amd64-static.tar.xz -C /ffmpeg --strip-components=1 && \
    rm /tmp/ffmpeg-release-amd64-static.tar.xz /tmp/ffmpeg-release-amd64-static.tar.xz.md5

# ==================================================
# Base runtime
# ==================================================
FROM python:3.11-slim AS base

RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        ca-certificates \
        libchromaprint-tools \
    && rm -rf /var/lib/apt/lists/* \
    && apt-get clean

COPY --from=ffmpeg-downloader /ffmpeg/ffmpeg /usr/local/bin/ffmpeg
COPY --from=ffmpeg-downloader /ffmpeg/ffprobe /usr/local/bin/ffprobe

RUN pip install --no-cache-dir yt-dlp && pip cache purge

# Owned by the runtime user: a named volume mounted on /tmp/.cache copies this
# ownership when it is first created, so yt-dlp can write its cache there.
RUN mkdir -p /config /music /tmp/ytmusic /logs /tmp/.cache && \
    chown 1000:1000 /music /tmp/ytmusic /logs /tmp/.cache

ENV HOME=/tmp \
    PATH="/usr/local/bin:${PATH}" \
    PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1

VOLUME ["/music", "/logs"]

# ==================================================
# CLI variant
# ==================================================
FROM base AS cli

COPY --from=builder /build/ytmusic /usr/local/bin/ytmusic
COPY config.example.yaml /etc/ytmusic/config.example.yaml

USER 1000:1000
WORKDIR /tmp/ytmusic
ENTRYPOINT ["ytmusic"]
CMD ["--help"]

# ==================================================
# Web variant
# ==================================================
FROM base AS web

RUN apt-get update && \
    apt-get install -y --no-install-recommends curl && \
    rm -rf /var/lib/apt/lists/* && \
    apt-get clean

COPY --from=builder /build/ytmusic /usr/local/bin/ytmusic
COPY --from=builder /build/ytmusic-web /usr/local/bin/ytmusic-web

COPY config.example.yaml /etc/ytmusic/config.example.yaml

USER 1000:1000
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD curl -f http://localhost:8080/api/health || exit 1

ENTRYPOINT ["ytmusic-web"]
CMD ["-config", "/config/config.yaml", "-port", "8080"]
