FROM golang:1.27-alpine AS builder

WORKDIR /build

COPY . .

# .git is not in the build context: the version comes from the caller.
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -ldflags="-s -w -X ytmusic/internal/buildinfo.Version=${VERSION}" -o . ./cmd/...

# Same Debian as the runtime image: ffmpeg links its libmp3lame and libopus.
FROM python:3.11-slim AS ffmpeg

RUN apt-get update && \
    apt-get install -y --no-install-recommends build-essential nasm pkg-config xz-utils \
        libmp3lame-dev libopus-dev zlib1g-dev && \
    rm -rf /var/lib/apt/lists/*

ADD --checksum=sha256:8c3850283eb25fa026482078a04051e0be17347b09ef81a0849bec15a96e002e \
    https://ffmpeg.org/releases/ffmpeg-9.0.2.tar.xz /src/ffmpeg.tar.xz

# Only what yt-dlp asks of ffmpeg for the six audio formats, cover conversion and metadata (CLAUDE.md).
WORKDIR /src
RUN tar -xJf ffmpeg.tar.xz --strip-components=1 && \
    ./configure --prefix=/ffmpeg \
        --disable-everything --disable-autodetect --disable-doc --disable-debug \
        --disable-ffplay --disable-avdevice --disable-network \
        --enable-libmp3lame --enable-libopus --enable-zlib \
        --enable-protocol=file,pipe \
        --enable-demuxer=mov,matroska,ogg,mp3,aac,flac,wav,image2,image_jpeg_pipe,image_png_pipe,image_webp_pipe,ffmetadata \
        --enable-muxer=mp3,ipod,mp4,adts,ogg,opus,flac,wav,image2,ffmetadata \
        --enable-decoder=aac,opus,vorbis,mp3,mp3float,flac,pcm_s16le,pcm_s24le,pcm_f32le,mjpeg,png,webp \
        --enable-encoder=libmp3lame,aac,libopus,flac,pcm_s16le,mjpeg,png \
        --enable-parser=aac,opus,vorbis,flac,mpegaudio,mjpeg,png,webp \
        --enable-bsf=aac_adtstoasc,mjpeg2jpeg \
        --enable-filter=aresample,aformat,anull,format,scale,null && \
    make -j"$(nproc)" && \
    make install

FROM alpine:3.24 AS fpcalc

ARG TARGETARCH
RUN case "$TARGETARCH" in \
        amd64) arch=x86_64 sum=fc16cd37a70168040bc9ceb45f1d4d1216f5a75bc4c9cf8564bea70ac6a45733 ;; \
        arm64) arch=arm64 sum=7eaf5d655c4aa172ab28e3c870b8bb61dd2c327ac94de145676f88842cf6215a ;; \
        *) echo "no static fpcalc for $TARGETARCH" >&2; exit 1 ;; \
    esac && \
    wget -qO /tmp/fpcalc.tar.gz "https://github.com/acoustid/chromaprint/releases/download/v1.6.1/chromaprint-fpcalc-1.6.1-linux-$arch.tar.gz" && \
    echo "$sum  /tmp/fpcalc.tar.gz" | sha256sum -c - && \
    tar -xzf /tmp/fpcalc.tar.gz -C /usr/local/bin --strip-components=1

FROM python:3.11-slim AS base

RUN apt-get update && \
    apt-get install -y --no-install-recommends ca-certificates libmp3lame0 libopus0 && \
    rm -rf /var/lib/apt/lists/*

COPY --from=ffmpeg /ffmpeg/bin/ffmpeg /ffmpeg/bin/ffprobe /usr/local/bin/
COPY --from=fpcalc /usr/local/bin/fpcalc /usr/local/bin/
# yt-dlp needs a JavaScript runtime for YouTube, and deno is the one it enables by default.
COPY --from=denoland/deno:bin-2.9.7 /deno /usr/local/bin/

# The default extras bring mutagen, without which the cover can't be embedded in m4a, opus and flac.
RUN pip install --no-cache-dir "yt-dlp[default]"

# A named volume on /tmp/.cache copies this owner when first created.
RUN mkdir -p /config /music /tmp/ytmusic /logs /tmp/.cache && \
    chown 1000:1000 /music /tmp/ytmusic /logs /tmp/.cache

ENV HOME=/tmp \
    PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1

FROM base AS cli

COPY --from=builder /build/ytmusic /usr/local/bin/ytmusic
COPY config.example.yaml /etc/ytmusic/config.example.yaml

USER 1000:1000
WORKDIR /tmp/ytmusic
ENTRYPOINT ["ytmusic"]
CMD ["--help"]

FROM base AS web

COPY --from=builder /build/ytmusic /build/ytmusic-web /usr/local/bin/
COPY config.example.yaml /etc/ytmusic/config.example.yaml

USER 1000:1000
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD ["python3", "-c", "import urllib.request; urllib.request.urlopen('http://localhost:8080/api/health', timeout=5)"]

ENTRYPOINT ["ytmusic-web"]
CMD ["-config", "/config/config.yaml", "-port", "8080"]
