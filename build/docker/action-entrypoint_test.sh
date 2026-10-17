#!/usr/bin/env bash
# Run from any directory with the built linter and jq on PATH.
set -euo pipefail
cd "$(dirname "$0")/../.."

work="$(mktemp -d "${TMPDIR:-/tmp}/otelcol-action-test.XXXXXX")"
trap 'rm -rf "${work}"' EXIT
# Keep local checks from writing to a surrounding GitHub Actions step.
export GITHUB_STEP_SUMMARY=""

{
  printf 'kind: ConfigMap\nmetadata:\n  name: agent\ndata:\n  config.yaml: |\n'
  sed 's/^/    /' testdata/rules/unknown-field.yaml
} >"${work}/embedded.yaml"
cp testdata/rules/unknown-component.yaml "${work}/unknown-component.yaml"
cat >"${work}/logging.yaml" <<'YAML'
receivers:
  otlp:
    protocols:
      grpc:
exporters:
  logging:
service:
  pipelines:
    traces:
      receivers: [otlp]
      exporters: [logging]
YAML

for flag in strict embedded ignore-missing-schemas verbose exit-on-error \
  collector-version distribution min-severity fail-on; do
  policies=(true false)
  rules='  default: none
  enable: [unknown-field, unknown-component]'
  case "${flag}" in
    strict)
      setting='run: {strict: VALUE}'
      files=(testdata/rules/unknown-field.yaml)
      when_first='.summary.invalid == 1'
      when_second='.summary.valid == 1 and .summary.warnings == 1'
      ;;
    embedded)
      setting='run: {embedded: VALUE}'
      files=("${work}/embedded.yaml")
      when_first='.summary.warnings == 1'
      when_second='(.summary.warnings // 0) == 0'
      ;;
    ignore-missing-schemas)
      setting='run: {ignoreMissingSchemas: VALUE}'
      # Explicitly enabling unknown-component overrides ignoreMissingSchemas.
      rules='  default: all'
      files=(testdata/rules/unknown-component.yaml)
      when_first='.summary.valid == 1'
      when_second='.summary.invalid == 1'
      ;;
    verbose)
      setting='output: {verbose: VALUE}'
      files=(testdata/valid/agent.yaml)
      when_first='.files | length == 1'
      # Parsed passing files carry coverage even without verbose output.
      when_second="${when_first}"
      ;;
    exit-on-error)
      setting='issues: {exitOnError: VALUE}'
      files=(testdata/rules/unknown-component.yaml "${work}/unknown-component.yaml")
      when_first='.summary.invalid == 1'
      when_second='.summary.invalid == 2'
      ;;
    collector-version)
      policies=(v0.110.0 latest)
      setting='run: {collectorVersion: VALUE}'
      files=("${work}/logging.yaml")
      when_first='.summary.valid == 1'
      when_second='.summary.invalid == 1'
      ;;
    distribution)
      policies=(otlp contrib)
      setting='run: {distribution: VALUE}'
      files=(testdata/valid/agent.yaml)
      when_first='.summary.invalid == 1'
      when_second='.summary.valid == 1'
      ;;
    min-severity)
      policies=(error info)
      setting='issues: {minSeverity: VALUE}'
      files=(testdata/rules/unknown-field.yaml)
      when_first='(.summary.warnings // 0) == 0'
      when_second='.summary.warnings == 1'
      ;;
    fail-on)
      policies=(warning error)
      setting='issues: {failOn: VALUE}'
      files=(testdata/rules/unknown-field.yaml)
      when_first='.summary.invalid == 1'
      when_second='.summary.valid == 1 and .summary.warnings == 1'
      ;;
  esac

  # Include the defaults GitHub actually supplies, not just script omissions.
  action_default="$(sed -n "/^  ${flag}:/,/^  [^ ]/s/^    default: //p" action.yml)"
  test -n "${action_default}"
  action_default="${action_default#\"}"
  action_default="${action_default%\"}"

  for policy in "${policies[@]}"; do
    printf '%s\nrules:\n%s\n' "${setting/VALUE/${policy}}" "${rules}" >"${work}/policy.yaml"

    # GitHub sends omitted inputs as blanks; direct script callers can omit
    # the argument altogether. Both must leave the settings file in charge.
    for input in omitted '' action-default "${policies[@]}"; do
      echo "${flag}: setting=${policy}, input=${input:-blank}"
      effective="${policy}"
      cli=(run --config "${work}/policy.yaml"
        --schema-location testdata/schemas --output json)
      action=("--config=${work}/policy.yaml"
        '--schema-location=testdata/schemas' '--output=json' "--files=${files[*]}")
      if [ "${flag}" != collector-version ]; then
        cli+=(--collector-version v0.157.0)
        action+=('--collector-version=v0.157.0')
      fi
      if [ "${input}" = action-default ]; then
        # The CLI leaves this flag unset; an Action default must inherit policy.
        action+=("--${flag}=${action_default}")
      elif [ "${input}" != omitted ]; then
        action+=("--${flag}=${input}")
        if [ -n "${input}" ]; then
          cli+=("--${flag}=${input}")
          effective="${input}"
        fi
      fi

      cli_code=0
      otelcol-config-lint "${cli[@]}" "${files[@]}" >"${work}/cli.json" 2>"${work}/cli.stderr" || cli_code=$?
      test "${cli_code}" -le 1
      predicate="${when_first}"
      if [ "${effective}" = "${policies[1]}" ]; then
        predicate="${when_second}"
      fi
      jq -e "${predicate}" "${work}/cli.json" >/dev/null

      if [ "${flag}" = verbose ]; then
        if [ "${effective}" = true ]; then
          grep -Fq "settings read from ${work}/policy.yaml" "${work}/cli.stderr"
        else
          test ! -s "${work}/cli.stderr"
        fi
      fi

      action_code=0
      : >"${work}/outputs"
      GITHUB_OUTPUT="${work}/outputs" bash build/docker/action-entrypoint.sh \
        "${action[@]}" >"${work}/action.json" 2>"${work}/action.stderr" || action_code=$?
      test "${action_code}" -eq "${cli_code}"
      diff -u "${work}/cli.json" "${work}/action.json"
      diff -u "${work}/cli.stderr" "${work}/action.stderr"
      grep -Fqx "exit-code=${cli_code}" "${work}/outputs"
    done
  done
done

# No settings file: all four string inputs must retain the CLI defaults.
defaults=()
for flag in collector-version distribution min-severity fail-on; do
  action_default="$(sed -n "/^  ${flag}:/,/^  [^ ]/s/^    default: //p" action.yml)"
  action_default="${action_default#\"}"
  action_default="${action_default%\"}"
  defaults+=("--${flag}=${action_default}")
done
cli_code=0
otelcol-config-lint run --no-config --schema-location testdata/schemas --output json \
  testdata/rules/unknown-field.yaml >"${work}/cli.json" || cli_code=$?
test "${cli_code}" -eq 0
jq -e '.summary.valid == 1 and .summary.warnings > 0' "${work}/cli.json" >/dev/null
action_code=0
GITHUB_OUTPUT="${work}/outputs" bash build/docker/action-entrypoint.sh \
  --no-config=true --schema-location=testdata/schemas --output=json \
  --files=testdata/rules/unknown-field.yaml "${defaults[@]}" \
  >"${work}/action.json" || action_code=$?
test "${action_code}" -eq "${cli_code}"
diff -u "${work}/cli.json" "${work}/action.json"

# A directory argument filters extensions; expanded globs become explicit files.
mkdir -p "${work}/candidates/nested"
for file in collector.yaml collector.yml nested/collector.YAML; do
  printf 'receivers: {}\n' >"${work}/candidates/${file}"
done
printf '%s\n' '-----BEGIN CERTIFICATE-----' >"${work}/candidates/beta-kafka-ca.pem"
printf '%s\n' 'not a config' >"${work}/candidates/nested/notes.txt"
for input in "${work}/candidates" "${work}/candidates/*"; do
  action_code=0
  bash build/docker/action-entrypoint.sh --no-config=true --default=none \
    --schema-location=testdata/schemas --output=json "--files=${input}" \
    >"${work}/action.json" || action_code=$?
  if [ "${input}" = "${work}/candidates" ]; then
    test "${action_code}" -eq 0
    jq -e '.files | length == 3 and all(.filename | endswith(".pem") | not)' "${work}/action.json" >/dev/null
  else
    test "${action_code}" -eq 1
    jq -e 'any(.files[]; (.filename | endswith(".pem")) and .status == "invalid")' "${work}/action.json" >/dev/null
  fi
done
