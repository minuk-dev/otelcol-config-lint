BINARY  := bin/otelcol-config-lint
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X github.com/minuk-dev/otelcol-config-lint/pkg/cmd/otelcol-config-lint.Version=$(VERSION)

# A checkout of github.com/minuk-dev/otelcol-config-schemas, which is where
# generated schemas are written.
SCHEMAS ?= ../otelcol-config-schemas

# Releases to regenerate schemas for, e.g. make schemas VERSIONS=v0.158.0
VERSIONS ?= $(shell jq -r '[.distributions[][]] | unique | join(",")' $(SCHEMAS)/index.json 2>/dev/null)

.PHONY: all build build-snapshot test lint lint-fix fmt schemas clean

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

# Regenerate the component schemas from the upstream collector sources.
schemas:
	go run ./tools/schemagen -version '$(VERSIONS)' -out '$(SCHEMAS)'

clean:
	rm -rf bin dist coverage.txt
