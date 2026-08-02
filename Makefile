BINARY  := bin/otelcol-config-lint
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X github.com/minuk-dev/otelcol-config-lint/pkg/cmd/otelcol-config-lint.Version=$(VERSION)

# Releases to regenerate catalogs for, e.g. make catalogs VERSIONS=v0.158.0
VERSIONS ?= $(shell jq -r '.distributions.all | join(",")' catalogs/index.json 2>/dev/null)

.PHONY: all build build-snapshot test lint lint-fix fmt catalogs clean

all: lint test build

build:
	go build -ldflags '$(LDFLAGS)' -o $(BINARY) ./cmd/otelcol-config-lint

# Build every release target locally, the way CI does before a tag.
build-snapshot:
	goreleaser build --snapshot --clean

test:
	go test -race ./... -coverprofile=coverage.txt

lint:
	golangci-lint run

lint-fix:
	golangci-lint run --fix

fmt:
	gofmt -w .

# Regenerate the component catalogs from the upstream collector sources.
catalogs:
	go run ./tools/schemagen -version '$(VERSIONS)'

clean:
	rm -rf bin dist coverage.txt
