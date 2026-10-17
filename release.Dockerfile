# Used by goreleaser, which supplies the prebuilt binary. Dockerfile builds from source.
FROM gcr.io/distroless/static-debian12:nonroot
ARG TARGETPLATFORM
COPY ${TARGETPLATFORM}/mcp-exporter /mcp-exporter
USER 65532:65532
ENTRYPOINT ["/mcp-exporter"]
