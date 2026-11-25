# syntax=docker/dockerfile:1

ARG GO_VERSION=1.27
ARG ALPINE_VERSION=3.22

# --- Go binary. The UI is committed static files (web/static, embedded via
# go:embed), so there is no frontend build step. Runs on the build host and
# cross-compiles (pure Go, no cgo), so multi-arch builds don't need QEMU. ---
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download
COPY . ./
# Tag is "v0.1.0" by convention; strip the "v" so --version and the
# MusicBrainz User-Agent report "0.1.0". Local builds default to "dev".
ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath \
      -ldflags="-s -w -X main.version=${VERSION#v}" \
      -o /out/coda ./cmd/coda

# --- Runtime ---
FROM alpine:${ALPINE_VERSION}
ARG VERSION=dev
LABEL org.opencontainers.image.title="coda" \
      org.opencontainers.image.description="Self-hosted music library manager" \
      org.opencontainers.image.source="https://github.com/PPabloMunoz/coda" \
      org.opencontainers.image.version="${VERSION}"

# Non-root by default (uid/gid 1000). docker-compose overrides this with
# PUID:PGID; /data and /music are pre-owned so named volumes work too.
RUN apk add --no-cache ca-certificates \
    && adduser -D -u 1000 coda \
    && mkdir -p /data /music \
    && chown coda:coda /data /music

COPY --from=build /out/coda /usr/local/bin/coda

# Sane defaults so `docker run -v ...:/music -v ...:/data` works unconfigured.
ENV CODA_PORT=8687 \
    CODA_ROOTS=/music \
    CODA_CONFIG=/data/config.yaml \
    CODA_DB_PATH=/data/coda.db \
    GIN_MODE=release

USER coda
WORKDIR /data
EXPOSE 8687

# Lives in the image so every runner (compose, k8s, plain docker) gets it;
# follows CODA_PORT instead of hardcoding 8687.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -qO- "http://127.0.0.1:${CODA_PORT}/api/v1/health" | grep -q ok || exit 1

ENTRYPOINT ["coda"]
