# The catalogs are embedded in the binary, so the image needs nothing but the
# binary itself and the CA bundle for fetching remote -catalog-location URLs.
FROM gcr.io/distroless/static-debian12:nonroot

COPY otelcol-config-lint /usr/local/bin/otelcol-config-lint

WORKDIR /workspace
USER nonroot:nonroot

ENTRYPOINT ["/usr/local/bin/otelcol-config-lint"]
CMD ["--help"]
