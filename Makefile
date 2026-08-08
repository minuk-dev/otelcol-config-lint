BINARY  := bin/otelcol-config-lint
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X github.com/minuk-dev/otelcol-config-lint/pkg/version.Version=$(VERSION)

# A checkout of github.com/minuk-dev/otelcol-config-schemas, which is where
# generated schemas are written.
SCHEMAS ?= ../otelcol-config-schemas

# A checkout of github.com/open-telemetry/opentelemetry-collector-releases,
# whose distributions/*/manifest.yaml describe the binaries upstream ships.
# Check it out at the release being generated: the manifests are what say which
# components that release's binaries carry.
RELEASES ?= ../opentelemetry-collector-releases

# The registry's distribution names and the manifest each one is built from.
# The names are the registry's own: upstream calls them otelcol and
# otelcol-contrib, and what a config is linted against is core and contrib.
BUILDERS ?= \
	core=$(RELEASES)/distributions/otelcol/manifest.yaml \
	contrib=$(RELEASES)/distributions/otelcol-contrib/manifest.yaml \
	k8s=$(RELEASES)/distributions/otelcol-k8s/manifest.yaml \
	otlp=$(RELEASES)/distributions/otelcol-otlp/manifest.yaml

.PHONY: all build build-snapshot verify-version test lint lint-fix fmt schemas clean

all: lint test build

build:
	go build -ldflags '$(LDFLAGS)' -o $(BINARY) ./cmd/otelcol-config-lint

# The linker ignores -X against a symbol that does not exist, so LDFLAGS naming
# a package that has moved fails silently and ships a binary reporting "dev".
# Build one and read the version back out.
verify-version: build
	@got="$$($(BINARY) version)"; want='otelcol-config-lint $(VERSION)'; \
	if [ "$$got" != "$$want" ]; then \
		echo "the version stamp did not land: got \"$$got\", want \"$$want\"" >&2; \
		echo 'check that LDFLAGS names the package Version actually lives in' >&2; \
		exit 1; \
	fi
	@echo 'version stamp lands: $(VERSION)'

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

# Regenerate the component schemas from the distributions' builder manifests.
schemas:
	go run ./cmd/schemagen $(addprefix --builder ,$(BUILDERS)) --registry '$(SCHEMAS)'

clean:
	rm -rf bin dist coverage.txt
