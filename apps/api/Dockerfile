# syntax=docker/dockerfile:1

# Set to "true" (--build-arg INSTALL_REEL_MEDIA=true) to bundle yt-dlp + ffmpeg
# (ffprobe ships with ffmpeg) for REEL_MEDIA=video deep-media downloads. Left
# at "false" the image is untouched: these binaries are optional and never a
# required dependency, per project principle (docs/self-hosting.md).
ARG INSTALL_REEL_MEDIA=false

# Build stage
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /openmind ./cmd/openmind
RUN mkdir -p /data/assets

# Runtime stage — default (INSTALL_REEL_MEDIA=false): unchanged, distroless.
# This is what the Dockerfile produced before reel-media support was added;
# nothing below is built unless the video variant is explicitly selected.
FROM gcr.io/distroless/static:nonroot AS runtime-false
COPY --from=build /openmind /openmind
# Pre-create the assets mount point owned by the nonroot user (uid/gid 65532)
# so a freshly-initialised named volume at /data/assets inherits writable
# ownership instead of defaulting to root:root.
COPY --from=build --chown=65532:65532 /data/assets /data/assets

# Runtime stage — opt-in (INSTALL_REEL_MEDIA=true): adds ffmpeg (which
# provides ffprobe) and yt-dlp for the REEL_MEDIA=video deep-media rung.
#
# distroless/static has no shell and no package manager, so there is no way
# to conditionally install into it — instead this is a wholly separate,
# Alpine-based runtime that is only ever built when explicitly selected.
# BuildKit resolves the FROM at the bottom of this file to exactly one of
# runtime-false/runtime-true, so the default build never builds this stage,
# downloads nothing extra, and adds no layers to the default image.
FROM alpine:3.20 AS runtime-true
RUN apk add --no-cache ffmpeg yt-dlp
COPY --from=build /openmind /openmind
COPY --from=build /data/assets /data/assets
RUN addgroup -g 65532 nonroot \
    && adduser -D -u 65532 -G nonroot -h /nonexistent -s /sbin/nologin nonroot \
    && chown -R 65532:65532 /data/assets
USER nonroot:nonroot

# Final stage: select the default or video-enabled runtime built above.
FROM runtime-${INSTALL_REEL_MEDIA} AS runtime
EXPOSE 8080
ENTRYPOINT ["/openmind"]
CMD ["all"]
