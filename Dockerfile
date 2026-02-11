# ==================================================
# Builder stage - compile Go binaries
# ==================================================
FROM golang:1.23-alpine AS builder

WORKDIR /build

RUN apk add --no-cache git

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -a -installsuffix cgo -o ytmusic ./cmd/ytmusic
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -a -installsuffix cgo -o ytmusic-web ./cmd/ytmusic-web

RUN apk add --no-cache upx && \
    upx --best --lzma ytmusic ytmusic-web

# ==================================================
# FFmpeg downloader - get static minimal build
# ==================================================
FROM alpine:3.19 AS ffmpeg-downloader

RUN apk add --no-cache curl xz && \
    curl -fL https://johnvansickle.com/ffmpeg/releases/ffmpeg-release-amd64-static.tar.xz -o /tmp/ffmpeg.tar.xz && \
    mkdir /ffmpeg && \
    tar -xf /tmp/ffmpeg.tar.xz -C /ffmpeg --strip-components=1 && \
    rm /tmp/ffmpeg.tar.xz

# ==================================================
# Base runtime
# ==================================================
FROM python:3.11-slim AS base

RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && apt-get clean

COPY --from=ffmpeg-downloader /ffmpeg/ffmpeg /usr/local/bin/ffmpeg
COPY --from=ffmpeg-downloader /ffmpeg/ffprobe /usr/local/bin/ffprobe

RUN pip install --no-cache-dir yt-dlp && pip cache purge

RUN mkdir -p /config /music /tmp/ytmusic /logs /root/.config/ytmusic

RUN echo "parallel_jobs: 4\n\
cookies_browser: brave\n\
audio_format: mp3\n\
verbose: false\n\
dry_run: false" > /root/.config/ytmusic/config.yaml

ENV HOME=/root \
    PATH="/usr/local/bin:${PATH}" \
    PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1

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

RUN apt-get update && \
    apt-get install -y --no-install-recommends curl && \
    rm -rf /var/lib/apt/lists/* && \
    apt-get clean

COPY --from=builder /build/ytmusic /usr/local/bin/ytmusic
COPY --from=builder /build/ytmusic-web /usr/local/bin/ytmusic-web

COPY web/static /app/web/static
COPY config.example.yaml /etc/ytmusic/config.example.yaml

WORKDIR /app
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD curl -f http://localhost:8080/api/jobs || exit 1

ENTRYPOINT ["ytmusic-web"]
CMD ["-port", "8080"]
