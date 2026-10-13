#!/usr/bin/env bash
# Run from any directory with absolute paths to the pinned binaries.
set -euo pipefail
: "${LINTER:?set LINTER to the pinned linter binary}"
: "${COLLECTOR:?set COLLECTOR to otelcol-contrib v0.157.0}"
cd "$(dirname "$0")/../.."

test "$("$COLLECTOR" --version)" = 'otelcol-contrib version 0.157.0'
printf '%s\n' '169c93378297fec8bf1d1cc0d54aa6ad029835228e5bb7e96796d89cd40454db  testdata/schemas/contrib/v0.157.0.json' |
  shasum -a 256 --check
"$LINTER" version
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

expect_exit() {
  local expected=$1 actual=0
  shift
  "$@" || actual=$?
  if [ "$actual" -ne "$expected" ]; then
    printf 'expected exit %s, got %s: %s\n' "$expected" "$actual" "$*" >&2
    return 1
  fi
}

# Embedded mode only scans ConfigMaps; checking plain YAML separately prevents skips.
expect_exit 1 "$LINTER" run --config testdata/rollout/settings.yaml \
  testdata/rollout/configs/agent.yaml testdata/rollout/configs/provider.yaml \
  testdata/rollout/configs/invalid-reference.yaml > "$work/plain.json"
expect_exit 0 "$LINTER" run --config testdata/rollout/settings.yaml --embedded \
  testdata/rollout/configs/gateway-configmap.yaml > "$work/embedded.json"
jq -s '[.[].files[] | {filename, status,
  findings: [(.diagnostics // [])[] | {rule, severity, path}]}] | sort_by(.filename)' \
  "$work/plain.json" "$work/embedded.json" > "$work/actual.json"
diff -u testdata/rollout/expected.json "$work/actual.json"

for config in agent provider; do
  expect_exit 0 env -i ROLLOUT_ENDPOINT=127.0.0.1:4317 ROLLOUT_RECEIVER=otlp \
    "$COLLECTOR" validate --config "$PWD/testdata/rollout/configs/$config.yaml"
done
expect_exit 1 env -i "$COLLECTOR" validate \
  --config "$PWD/testdata/rollout/configs/invalid-reference.yaml" > "$work/invalid.log" 2>&1
grep -F 'references receiver "missing" which is not configured' "$work/invalid.log"
# Extract this fixture's single literal data block, not arbitrary Kubernetes YAML.
sed '1,/^  collector.yaml: |$/d; s/^    //' \
  testdata/rollout/configs/gateway-configmap.yaml > "$work/gateway.yaml"
expect_exit 0 env -i "$COLLECTOR" validate --config "$work/gateway.yaml"

# A static pass cannot validate a reference supplied at runtime.
expect_exit 1 env -i ROLLOUT_ENDPOINT=127.0.0.1:4317 ROLLOUT_RECEIVER=missing \
  "$COLLECTOR" validate --config "$PWD/testdata/rollout/configs/provider.yaml" > "$work/provider.log" 2>&1
grep -F 'references receiver "missing" which is not configured' "$work/provider.log"
expect_exit 0 env -i ROLLOUT_ENDPOINT=127.0.0.1:4317 ROLLOUT_RECEIVER=missing \
  "$LINTER" run --config testdata/rollout/settings.yaml \
  testdata/rollout/configs/provider.yaml > "$work/provider.json"
jq -e '.files | length == 1 and all(.[]; .status == "valid")' "$work/provider.json" > /dev/null

# Container sizing is policy evidence, independent of Collector config validity.
expect_exit 1 "$LINTER" run --config testdata/rollout/settings.yaml \
  --memory-request 128Mi --memory-limit 128Mi \
  testdata/rollout/configs/agent.yaml > "$work/memory.json"
jq -e 'any(.files[].diagnostics[];
  .rule == "memory-limiter-sizing" and .severity == "error")' "$work/memory.json" > /dev/null
printf '%s\n' 'PASS: expected findings, Collector validation, provider gap and memory policy control'
