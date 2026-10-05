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

for flag in strict embedded ignore-missing-schemas verbose exit-on-error; do
  rules='  default: none
  enable: [unknown-field, unknown-component]'
  case "${flag}" in
    strict)
      setting='run: {strict: BOOL}'
      files=(testdata/rules/unknown-field.yaml)
      when_true='.summary.invalid == 1'
      when_false='.summary.valid == 1 and .summary.warnings == 1'
      ;;
    embedded)
      setting='run: {embedded: BOOL}'
      files=("${work}/embedded.yaml")
      when_true='.summary.warnings == 1'
      when_false='(.summary.warnings // 0) == 0'
      ;;
    ignore-missing-schemas)
      setting='run: {ignoreMissingSchemas: BOOL}'
      # Explicitly enabling unknown-component overrides ignoreMissingSchemas.
      rules='  default: all'
      files=(testdata/rules/unknown-component.yaml)
      when_true='.summary.valid == 1'
      when_false='.summary.invalid == 1'
      ;;
    verbose)
      setting='output: {verbose: BOOL}'
      files=(testdata/valid/agent.yaml)
      when_true='.files | length == 1'
      when_false='.files | length == 0'
      ;;
    exit-on-error)
      setting='issues: {exitOnError: BOOL}'
      files=(testdata/rules/unknown-component.yaml "${work}/unknown-component.yaml")
      when_true='.summary.invalid == 1'
      when_false='.summary.invalid == 2'
      ;;
  esac

  for policy in true false; do
    printf '%s\nrules:\n%s\n' "${setting/BOOL/${policy}}" "${rules}" >"${work}/policy.yaml"

    # GitHub sends omitted inputs as blanks; direct script callers can omit
    # the argument altogether. Both must leave the settings file in charge.
    for input in omitted '' true false; do
      echo "${flag}: setting=${policy}, input=${input:-blank}"
      effective="${policy}"
      cli=(run --config "${work}/policy.yaml" --collector-version v0.157.0
        --schema-location testdata/schemas --output json)
      action=("--config=${work}/policy.yaml" '--collector-version=v0.157.0'
        '--schema-location=testdata/schemas' '--output=json' "--files=${files[*]}")
      if [ "${input}" != omitted ]; then
        action+=("--${flag}=${input}")
        if [ -n "${input}" ]; then
          cli+=("--${flag}=${input}")
          effective="${input}"
        fi
      fi

      cli_code=0
      otelcol-config-lint "${cli[@]}" "${files[@]}" >"${work}/cli.json" || cli_code=$?
      test "${cli_code}" -le 1
      predicate="${when_true}"
      if [ "${effective}" = false ]; then
        predicate="${when_false}"
      fi
      jq -e "${predicate}" "${work}/cli.json" >/dev/null

      action_code=0
      : >"${work}/outputs"
      GITHUB_OUTPUT="${work}/outputs" bash build/docker/action-entrypoint.sh \
        "${action[@]}" >"${work}/action.json" || action_code=$?
      test "${action_code}" -eq "${cli_code}"
      diff -u "${work}/cli.json" "${work}/action.json"
      grep -Fqx "exit-code=${cli_code}" "${work}/outputs"
    done
  done
done
