#!/usr/bin/env bash
#
# Entrypoint for the otelcol-config-lint GitHub Action.
#
# Turns the action's inputs into "otelcol-config-lint run" flags, publishes the
# summary counts as step outputs, and exits with the linter's own exit code so
# the step fails on 1 and reports a usage error distinctly on 2.

set -euo pipefail

in_files="."
in_collector_version=""
in_distribution=""
in_schema_location=""
in_strict="false"
in_ignore_missing_schemas="false"
in_min_severity=""
in_fail_on=""
in_disable=""
in_severity=""
in_exclude=""
in_output="github"
in_config=""
in_summary="true"
in_verbose="false"
in_exit_on_error="false"

# Inputs arrive as --name=value, one per input declared in action.yml, so the
# script does not depend on the order action.yml lists them in.
for arg in "$@"; do
  if [ "${arg#*=}" = "${arg}" ]; then
    echo "::error title=otelcol-config-lint::unexpected argument ${arg}"
    exit 2
  fi

  name="${arg%%=*}"
  value="${arg#*=}"

  case "${name}" in
    --files) in_files="${value:-.}" ;;
    --collector-version) in_collector_version="${value}" ;;
    --distribution) in_distribution="${value}" ;;
    --schema-location) in_schema_location="${value}" ;;
    --strict) in_strict="${value}" ;;
    --ignore-missing-schemas) in_ignore_missing_schemas="${value}" ;;
    --min-severity) in_min_severity="${value}" ;;
    --fail-on) in_fail_on="${value}" ;;
    --disable) in_disable="${value}" ;;
    --severity) in_severity="${value}" ;;
    --exclude) in_exclude="${value}" ;;
    --output) in_output="${value:-github}" ;;
    --config) in_config="${value}" ;;
    --summary) in_summary="${value}" ;;
    --verbose) in_verbose="${value}" ;;
    --exit-on-error) in_exit_on_error="${value}" ;;
    *)
      echo "::error title=otelcol-config-lint::unexpected argument ${arg}"
      exit 2
      ;;
  esac
done

flags=()

# value <flag> <input>: pass the flag only when the input was given, so the
# linter's own defaults stay in charge of everything left blank.
value() {
  if [ -n "$2" ]; then
    flags+=("--$1" "$2")
  fi
}

# toggle <flag> <input>: boolean inputs arrive as the strings true and false.
toggle() {
  if [ "$2" = "true" ]; then
    flags+=("--$1")
  fi
}

value collector-version "${in_collector_version}"
value distribution "${in_distribution}"
value min-severity "${in_min_severity}"
value fail-on "${in_fail_on}"
value disable "${in_disable}"
value severity "${in_severity}"
value exclude "${in_exclude}"
value config "${in_config}"
toggle strict "${in_strict}"
toggle ignore-missing-schemas "${in_ignore_missing_schemas}"
toggle verbose "${in_verbose}"
toggle exit-on-error "${in_exit_on_error}"

# --schema-location is repeatable: one location per line, searched in order.
while IFS= read -r location; do
  location="${location#"${location%%[![:space:]]*}"}"
  location="${location%"${location##*[![:space:]]}"}"

  if [ -n "${location}" ]; then
    flags+=(--schema-location "${location}")
  fi
done <<<"${in_schema_location}"

# Deliberately unquoted: files are separated by whitespace, and a glob such as
# configs/*.yaml is expanded here, against the workspace.
# shellcheck disable=SC2206
files=(${in_files})
if [ ${#files[@]} -eq 0 ]; then
  files=(.)
fi

# The counts come from the JSON report whatever format the caller asked for, so
# it is written to a file first and the requested format rendered afterwards.
# The second pass re-reads the schema, from the registry over the network when
# no --schema-location is given; one schema for one release is a small download,
# and it buys the same numbers whichever format was asked for.
report="${TMPDIR:-/tmp}/otelcol-config-lint.json"

code=0
otelcol-config-lint run "${flags[@]}" --output json "${files[@]}" >"${report}" || code=$?

# A run that never produced a report -- a usage error -- still has to report a
# number, so anything unreadable counts as zero.
count() {
  local n
  n="$(jq -r ".summary.$1 // 0" "${report}" 2>/dev/null || true)"

  if [ -z "${n}" ] || [ "${n}" = "null" ]; then
    n=0
  fi

  printf '%s\n' "${n}"
}

emit_outputs() {
  if [ -z "${GITHUB_OUTPUT:-}" ]; then
    return
  fi

  {
    echo "exit-code=$1"
    echo "valid=$(count valid)"
    echo "invalid=$(count invalid)"
    echo "errors=$(count errors)"
    echo "skipped=$(count skipped)"
    echo "warnings=$(count warnings)"
    echo "infos=$(count infos)"
  } >>"${GITHUB_OUTPUT}"
}

# Exit code 2 means the run never happened -- the linter has already said why on
# stderr, and rendering it a second time would only repeat the message.
if [ "${code}" -eq 2 ]; then
  emit_outputs 2
  echo "::error title=otelcol-config-lint::the command could not run; check the action's inputs"
  exit 2
fi

if [ "${in_output}" = "json" ]; then
  emit_outputs "${code}"
  cat "${report}"
  exit "${code}"
fi

if [ "${in_summary}" = "true" ]; then
  flags+=(--summary)
fi

# The findings themselves. Only a usage error can differ from the pass above --
# an output format the linter does not know -- and that has to fail the step
# rather than be swallowed, or "output: xml" would silently report nothing.
render=0
otelcol-config-lint run "${flags[@]}" --output "${in_output}" "${files[@]}" || render=$?

if [ "${render}" -eq 2 ]; then
  emit_outputs 2
  echo "::error title=otelcol-config-lint::the command could not run; check the action's inputs"
  exit 2
fi

emit_outputs "${code}"

exit "${code}"
