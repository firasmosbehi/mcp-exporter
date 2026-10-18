#!/bin/sh
# OTLP Profiles is Alpha and its wire format changes between Collector
# releases, so the profilers must run the same Collector version as the one
# bundled in grafana/otel-lgtm (a mismatch fails as "proto: wrong wireType").
# Fails with the exact versions to set when they drift.
set -eu
cd "$(dirname "$0")/../.."
lgtm=$(sed -n 's/^FROM //p' deploy/compose/lgtm.Dockerfile)
want=$(docker run --rm --entrypoint /otel-lgtm/otelcol-contrib/otelcol-contrib "$lgtm" --version | awk '{print $NF}')
bad=0
for f in deploy/compose/profiling.yaml deploy/kubernetes/ebpf-profiler.yaml; do
  for img in $(grep -oE 'otel/opentelemetry-collector-[a-z-]+:[0-9][0-9.]*' "$f" | sort -u); do
    if [ "${img##*:}" != "$want" ]; then
      echo "MISMATCH $f: $img, but $lgtm bundles Collector $want: set it to ${img%:*}:$want"; bad=1
    fi
  done
done
[ $bad = 0 ] && echo "profiles versions aligned: Collector $want ($lgtm)"
exit $bad
