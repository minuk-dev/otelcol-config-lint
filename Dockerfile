# The image behind action.yml. It sits at the repository root because a
# container action builds with the Dockerfile's own directory as the context,
# and this build needs the whole source tree; build/docker/Dockerfile is the
# separate distroless image that releases publish.
#
# Unlike that one this image needs a shell, so the entrypoint can turn the
# action's inputs into flags and report the counts back as step outputs.
FROM golang:1.25-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -ldflags "-s -w" -o /out/otelcol-config-lint ./cmd/otelcol-config-lint

FROM alpine:3.22

# bash for the entrypoint's arrays, jq to read the summary out of the report,
# and the CA bundle because schemas are fetched from the published registry
# over HTTPS unless the workflow points schema-location somewhere local.
RUN apk add --no-cache bash jq ca-certificates

COPY --from=build /out/otelcol-config-lint /usr/local/bin/otelcol-config-lint
COPY build/docker/action-entrypoint.sh /usr/local/bin/action-entrypoint

WORKDIR /github/workspace

ENTRYPOINT ["/usr/local/bin/action-entrypoint"]
