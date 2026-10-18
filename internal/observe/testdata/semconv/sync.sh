#!/bin/sh
# Refreshes the pinned semantic-conventions registry files used by
# TestEmittedNamesAreInSemconv. GENAI_REF / CORE_REF default to the pins in
# SOURCE; pass GENAI_REF=main CORE_REF=main to compare against upstream.
#   internal/observe/testdata/semconv/sync.sh [outdir]
set -eu
here=$(cd "$(dirname "$0")" && pwd)
out=${1:-$here}
. "$here/SOURCE"
GENAI_REF=${GENAI_REF:-$GENAI_PIN}
CORE_REF=${CORE_REF:-$CORE_PIN}
mkdir -p "$out"
fetch() { curl -fsSL "https://raw.githubusercontent.com/open-telemetry/$1/$2/model/$3" -o "$out/$(echo "$1-$3" | tr / _)"; }
for f in mcp/registry.yaml mcp/metrics.yaml mcp/spans.yaml gen-ai/registry.yaml; do fetch semantic-conventions-genai "$GENAI_REF" "$f"; done
for f in jsonrpc/registry.yaml rpc/registry.yaml error/registry.yaml network/registry.yaml server/registry.yaml client/registry.yaml; do fetch semantic-conventions "$CORE_REF" "$f"; done
echo "fetched semconv: genai@$GENAI_REF core@$CORE_REF -> $out"
