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
FROM ghcr.io/minuk-dev/otelcol-config-lint:0.1.1 AS bin

FROM alpine:3.22

# bash for the entrypoint's arrays, jq to read the summary out of the report,
# and the CA bundle because schemas are fetched from the published registry
# over HTTPS unless the workflow points schema-location somewhere local.
#
# bash and jq are pinned with apk's prefix operator, at the minor rather than
# the patch. An exact pin is the wrong shape here: action.yml says
# `image: Dockerfile`, so this is rebuilt on every consumer's runner on every
# run, and Alpine's repository carries only the current revision of a package.
# The day 3.22 rebuilds bash for a CVE, an exact pin stops resolving and every
# workflow using this action fails on `apk add` -- and no automation in this
# repository bumps it back: dependabot reads go.mod and workflow `uses:` lines,
# not apk constraints.
#
# The pin used to name the patch too, on the reasoning that a prefix pin "still
# fails loudly if the version itself ever moves, which within one Alpine stable
# branch it does not". It does. On 2026-09-02 v3.22 moved jq from 1.8.1 to
# 1.8.2, `jq~1.8.1` stopped resolving, and the action failed on `apk add` for
# everyone running it -- the build breaks in the consumer's workflow, not here,
# which is the failure this pin was shaped to avoid in the first place. A
# stable branch holds the major and minor; the patch is not a promise, so the
# pin no longer asks for one.
#
# ca-certificates is deliberately left unpinned. Its version is the date the
# bundle was cut, so a pin of either shape freezes the trust store at a set of
# roots -- and breaks the build outright the day a new bundle lands.
RUN apk add --no-cache 'bash~5.2' 'jq~1.8' ca-certificates

COPY --from=bin /usr/local/bin/otelcol-config-lint /usr/local/bin/otelcol-config-lint
COPY build/docker/action-entrypoint.sh /usr/local/bin/action-entrypoint

WORKDIR /github/workspace

# No USER, deliberately, however much the released image's distroless nonroot
# invites one here. GitHub's own Dockerfile guidance for container actions is
# "do not use the USER instruction in your Dockerfile, because you won't be
# able to access the GITHUB_WORKSPACE directory", and the runner backs that up:
# $GITHUB_OUTPUT is a file the runner process creates on the host and mounts
# in, owned by the uid the runner runs as and not group- or world-writable, so
# any other uid gets EACCES appending to it. The entrypoint writes its counts
# there under `set -e`, which would turn a clean lint into a failed step for
# every consumer. Dropping privileges here needs the runner to stop handing out
# files only root can write, not a line in this file.
ENTRYPOINT ["/usr/local/bin/action-entrypoint"]
