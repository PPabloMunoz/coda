# Coda justfile. Go backend + Astro UI.

default:
    @just --list

build: web-build
    @mkdir -p bin
    go build -o bin/coda ./cmd/coda

run: build
    @clear
    ./bin/coda

build-prod: web-build
    @mkdir -p bin
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/coda ./cmd/coda

web-build:
    pnpm --dir web install --frozen-lockfile
    pnpm --dir web build

web-dev:
    pnpm --dir web dev --host

web-lint:
    pnpm --dir web lint

web-fmt:
    pnpm --dir web fmt

web-fmt-check:
    pnpm --dir web fmt:check

test:
    go test ./... -cover

vet:
    go vet ./...

lint:
    golangci-lint run ./...

fmt: web-fmt
    golangci-lint fmt ./...

fmt-check:
    @set -e; \
    out="$(golangci-lint fmt --diff ./...)"; \
    if [ -n "$out" ]; then \
        echo "$out"; \
        echo "Run: just fmt"; \
        exit 1; \
    fi

tidy:
    go mod tidy
    go mod vendor

check: fmt-check vet lint test web-lint web-fmt-check
    go build ./...

clean:
    rm -f ./bin/coda
    go clean -testcache
    rm -f coverage.out
    @if [ -d web/dist ]; then git clean -fdX web/dist; fi

db-reset:
    @test "$(uname -s)" = Darwin || { echo "db-reset is macOS-only"; exit 1; }
    rm -f "$HOME/Library/Application Support/Coda/"*.db*
