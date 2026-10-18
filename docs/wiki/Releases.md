# Releases

- **Versioning:** Semantic Versioning. Each release's notes come from its [CHANGELOG](https://github.com/firasmosbehi/mcp-exporter/blob/main/CHANGELOG.md) section.
- **Pre-releases:** tags like `v1.2.0-rc1` become GitHub pre-releases and **never move** the image's `latest` tag.
- **CI gate:** every release first runs the full CI: unit tests, race detector, fuzzing, Windows, and the end-to-end stack including profiles.

| Artifact | Where |
|---|---|
| Binaries (linux/darwin/windows × amd64/arm64), SBOMs, `checksums.txt` | [GitHub releases](https://github.com/firasmosbehi/mcp-exporter/releases) |
| Container image (multi-arch, distroless, non-root) | `ghcr.io/firasmosbehi/mcp-exporter:<version>` and `latest` |
| Helm chart | `oci://ghcr.io/firasmosbehi/charts/mcp-exporter --version <version>` |

## Verify

Everything is signed with keyless cosign by the release workflow:

```sh
V=0.4.0
ID="https://github.com/firasmosbehi/mcp-exporter/.github/workflows/release.yml@refs/tags/v$V"
ISSUER=https://token.actions.githubusercontent.com
cosign verify ghcr.io/firasmosbehi/mcp-exporter:$V --certificate-identity "$ID" --certificate-oidc-issuer $ISSUER
cosign verify ghcr.io/firasmosbehi/charts/mcp-exporter:$V --certificate-identity "$ID" --certificate-oidc-issuer $ISSUER
cosign verify-blob checksums.txt --signature checksums.txt.sig --certificate checksums.txt.pem \
  --certificate-identity "$ID" --certificate-oidc-issuer $ISSUER
sha256sum -c checksums.txt --ignore-missing
```

## Build provenance (SLSA)

Every archive, the image and the chart carry a GitHub artifact attestation recording the workflow, commit and runner that
built them. The image and chart attestations are also pushed to the registry next to the artifacts.

```sh
gh attestation verify mcp-exporter_${V}_linux_amd64.tar.gz -R firasmosbehi/mcp-exporter
gh attestation verify oci://ghcr.io/firasmosbehi/mcp-exporter:$V -R firasmosbehi/mcp-exporter
gh attestation verify oci://ghcr.io/firasmosbehi/charts/mcp-exporter:$V -R firasmosbehi/mcp-exporter
```
