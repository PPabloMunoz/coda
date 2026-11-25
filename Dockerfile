# syntax=docker/dockerfile:1

ARG GO_VERSION=1.27
ARG ALPINE_VERSION=3.21

FROM golang:${GO_VERSION}-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . ./
# Tag is "v0.1.0" by convention; strip the "v" so --version and the
# MusicBrainz User-Agent report "0.1.0". Local builds default to "dev".
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath \
  -ldflags="-s -w -X main.version=${VERSION#v}" \
  -o /out/coda ./cmd/coda

FROM alpine:${ALPINE_VERSION}
RUN apk add --no-cache ca-certificates \
  && mkdir -p /data /music
COPY --from=build /out/coda /usr/local/bin/coda
EXPOSE 8687
ENTRYPOINT ["coda"]
