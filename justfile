# Coda justfile. Go backend + vanilla static UI (web/static).

default:
    @just --list

build:
    @mkdir -p bin
    go build -o bin/coda ./cmd/coda

run: build
    @clear
    ./bin/coda

build-prod:
    @mkdir -p bin
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/coda ./cmd/coda

test:
    go test ./... -cover

test-race:
    go test -race ./...

vuln:
    go run golang.org/x/vuln/cmd/govulncheck@v1.10.0 ./...

vet:
    go vet ./...

lint:
    golangci-lint run ./...

fmt:
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

check: fmt-check vet lint test
    go build ./...

clean:
    rm -f ./bin/coda
    go clean -testcache
    rm -f coverage.out

db-reset:
    @test "$(uname -s)" = Darwin || { echo "db-reset is macOS-only"; exit 1; }
    rm -f "$HOME/Library/Application Support/Coda/"*.db*
