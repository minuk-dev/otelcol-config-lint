BINARY := bin/otelcol-config-lint

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

.PHONY: all build build-snapshot test lint lint-fix fmt schemas release-pin clean

all: lint test build

# No -ldflags for the version: the toolchain stamps the module version and the
# repository state into the binary, and pkg/version reads that back.
build:
	go build -o $(BINARY) ./cmd/otelcol-config-lint

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

# Point the action's image at the release about to be cut. A tag cannot
# reference an image built from itself, so this runs first and the tag goes on
# the commit it produces; the release workflow refuses a tag whose pin says
# anything else.
#
#   make release-pin RELEASE=v1.2.3
#   git commit -am 'chore(release): pin the action at v1.2.3'
#   git tag v1.2.3 && git push origin main v1.2.3
release-pin:
	@test -n '$(RELEASE)' || { echo 'usage: make release-pin RELEASE=v1.2.3' >&2; exit 1; }
	sed -i.bak -E 's|^(FROM ghcr\.io/minuk-dev/otelcol-config-lint):[^ ]*|\1:$(RELEASE:v%=%)|' Dockerfile
	@rm -f Dockerfile.bak
	@echo 'The action now runs $(RELEASE:v%=%); commit that and tag the commit $(RELEASE).'

# Regenerate the component schemas from the distributions' builder manifests.
schemas:
	go run ./cmd/schemagen generate $(addprefix --builder ,$(BUILDERS)) --registry '$(SCHEMAS)'

clean:
	rm -rf bin dist coverage.txt
