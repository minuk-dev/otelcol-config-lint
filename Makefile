BINARY  := bin/otelcol-config-lint
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X github.com/minuk-dev/otel-collector-config-linter/internal/cli.Version=$(VERSION)

# Releases to regenerate catalogs for, e.g. make catalogs VERSIONS=v0.158.0
VERSIONS ?= $(shell ls catalogs/*.yaml 2>/dev/null | xargs -n1 basename | sed 's/\.yaml$$//' | paste -sd, -)

.PHONY: all build test lint fmt catalogs clean

all: lint test build

build:
	go build -ldflags '$(LDFLAGS)' -o $(BINARY) ./cmd/otelcol-config-lint

test:
	go test ./...

lint:
	@out=$$(gofmt -l . | grep -v '^catalogs/' || true); \
	if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi
	go vet ./...

fmt:
	gofmt -w .

# Regenerate the component catalogs from the upstream collector sources.
catalogs:
	go run ./tools/schemagen -version '$(VERSIONS)'

clean:
	rm -rf bin
