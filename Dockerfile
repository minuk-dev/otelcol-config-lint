# The image behind action.yml. It sits at the repository root because a
# container action builds with the Dockerfile's own directory as the context.
#
# Nothing is compiled here. The linter is copied out of the image goreleaser
# published for the release this revision ships in, so a consumer's workflow
# pulls two small images instead of fetching a Go toolchain and rebuilding the
# binary before a single config is checked -- and what runs is the binary that
# was released, not one built from whatever the runner checked out.
#
# That image is distroless and has no shell, so it cannot be the action's image
# on its own: the entrypoint below is what turns inputs into flags and writes
# the counts back as step outputs.
#
# The pin is a release, never `latest`: a workflow pinned to @v1 must not change
# behaviour the moment the next release is cut. It names the goreleaser tag,
# which carries no leading v.
#
# A tag cannot reference an image built from itself, so the pin is bumped before
# the tag is cut, and the tag goes on the commit that carries the bump:
#
#     make release-pin RELEASE=v1.2.3
#
# The release workflow refuses a tag whose pin names anything else. Pull
# requests predate the release they are pinned to, so
# .github/workflows/action.yaml builds this image from source and rewrites the
# pin to it before using the action -- the source build stays reachable, it is
# just no longer what consumers run.
FROM ghcr.io/minuk-dev/otelcol-config-lint:1.0.0 AS bin

FROM alpine:3.22

# bash for the entrypoint's arrays, jq to read the summary out of the report,
# and the CA bundle because schemas are fetched from the published registry
# over HTTPS unless the workflow points schema-location somewhere local.
RUN apk add --no-cache bash jq ca-certificates

COPY --from=bin /usr/local/bin/otelcol-config-lint /usr/local/bin/otelcol-config-lint
COPY build/docker/action-entrypoint.sh /usr/local/bin/action-entrypoint

WORKDIR /github/workspace

ENTRYPOINT ["/usr/local/bin/action-entrypoint"]
