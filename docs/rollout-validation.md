# Rollout validation: observation before enforcement

This is the reproducible observation example for [#170](https://github.com/minuk-dev/otelcol-config-lint/issues/170),
not approval to make linting a required deployment gate. The committed corpus is
synthetic, written for this example: no deployed configuration or credentials
were collected. Maintainers must supply sanitized representative workloads and
approve a release and blocking policy before completing that issue.

## Pinned inputs

| Input | Pin used in this record |
| --- | --- |
| Published linter baseline | `v0.1.0`, source commit `a4f6cb28985ecb0601587d921dd7d5e5a143f2ce` |
| Candidate linter | `ca70f938a6e5c549d97bba004c4fba8703d6569c` (unreleased source snapshot) |
| Collector | Official `otelcol-contrib v0.157.0` |
| Offline schema | `testdata/schemas/contrib/v0.157.0.json` from the candidate commit |
| Schema SHA-256 | `169c93378297fec8bf1d1cc0d54aa6ad029835228e5bb7e96796d89cd40454db` |
| Policy and expected findings | [`settings.yaml`](../testdata/rollout/settings.yaml), [`expected.json`](../testdata/rollout/expected.json) |

The published baseline lacks `--embedded` and reports a false undefined reference
for the environment-provided receiver in this corpus. It cannot reproduce the
candidate's verdicts. A release containing the relevant correctness fixes is a
prerequisite for enforcement; a commit pin here is explicitly an observation
candidate, not a claim that those fixes have shipped. The baseline Action's
Dockerfile also points at image tag `1.0.0` rather than `0.1.0`; the Action needs
a correctly pinned release and image verification before adoption.

The schema is an existing full vendored snapshot, not a reduced schema invented
for the examples. `schemaLocations` names only that local file and `noCache` is
enabled, so linting needs no schema network access. Copy the schema, settings,
corpus and expectations together when reproducing outside this checkout.

## Corpus and expected decisions

All paths below are under [`testdata/rollout/configs`](../testdata/rollout/configs).
The memory values describe this synthetic scenario; they were not extracted
from an actual deployment. Endpoints use loopback or the reserved `.invalid`
domain and there are no authorization headers, keys, certificates or tokens.

| Case | Request / limit | Expected candidate lint / Collector `validate` |
| --- | --- | --- |
| `agent.yaml` | 256Mi / 256Mi | pass / pass; info about the in-memory sending queue |
| `gateway-configmap.yaml` | 512Mi / 512Mi | pass / pass after extracting `data.collector.yaml`; same queue info |
| `provider.yaml`, receiver environment value `otlp` | 256Mi / 256Mi | pass / pass; same queue info |
| `invalid-reference.yaml` | 256Mi / 256Mi | reject / reject; `undefined-reference`, plus unused receiver warning and queue info |
| `provider.yaml`, receiver environment value `missing` | 256Mi / 256Mi | pass / reject; known static-analysis gap |
| `agent.yaml` with memory overridden to 128Mi / 128Mi | 128Mi / 128Mi | reject / pass; container sizing policy is outside Collector config validation |

The provider case uses only `ROLLOUT_ENDPOINT=127.0.0.1:4317` and
`ROLLOUT_RECEIVER=otlp` or `missing`. Static linting deliberately does not resolve
runtime provider references. Custom distributions and external providers such as
file/HTTP/secret stores are **not covered**; obtain a sanitized builder manifest,
matching schema and controlled provider inputs if the team uses them. Do not
hide that gap with `ignoreMissingSchemas`.

## Reproduce the candidate observation

Prerequisites: Bash, Go matching `go.mod`, Git, curl, jq and `shasum`.
Run from this repository's root. Build the candidate from its exact source,
keeping this checkout's corpus and policy in place:

```sh
source_dir=$(mktemp -d)
git worktree add --detach "$source_dir" ca70f938a6e5c549d97bba004c4fba8703d6569c
(cd "$source_dir" && go build -trimpath -o /tmp/rollout-linter ./cmd/otelcol-config-lint)
export LINTER=/tmp/rollout-linter
```

Download the official Collector and verify the published archive checksum before
extracting. This example uses the platform recorded below; select the archive
for your platform when reproducing elsewhere:

```sh
platform=darwin_arm64
collector_dir=$(mktemp -d)
archive="otelcol-contrib_0.157.0_${platform}.tar.gz"
release=https://github.com/open-telemetry/opentelemetry-collector-releases/releases/download/v0.157.0
curl --fail --location --retry 3 "$release/$archive" --output "$collector_dir/$archive"
curl --fail --location --retry 3 \
  "$release/opentelemetry-collector-releases_otelcol-contrib_checksums.txt" \
  --output "$collector_dir/checksums.txt"
(cd "$collector_dir" && grep " ${archive}$" checksums.txt | shasum -a 256 --check)
tar -xzf "$collector_dir/$archive" -C "$collector_dir" otelcol-contrib
export COLLECTOR="$collector_dir/otelcol-contrib"
bash testdata/rollout/check.sh > /tmp/rollout-validation.log 2>&1
cat /tmp/rollout-validation.log
```

Binary acquisition/building needs network access unless dependencies and archives
are already available. Subsequent checks use local schemas and `Collector validate`;
they do not start listeners or connect to the fake exporters. The script checks
the Collector identity, schema checksum, both tools' exit codes, exact normalized
findings and the two intentional disagreements. A missing, skipped or extra result
fails the expectation diff. Plain configs and ConfigMaps run separately because
`--embedded` skips plain configs. Its literal-block extraction is specific to
this single ConfigMap fixture; extract each data key properly for a real corpus.

For the published baseline comparison, build/install the pinned release and run
the plain cases without candidate-only settings:

```sh
baseline_dir=$(mktemp -d)
GOBIN="$baseline_dir" go install github.com/minuk-dev/otelcol-config-lint/cmd/otelcol-config-lint@v0.1.0
"$baseline_dir/otelcol-config-lint" run --no-config --strict --no-cache \
  --collector-version v0.157.0 --distribution contrib \
  --schema-location testdata/schemas/contrib/v0.157.0.json \
  --memory-request 256Mi --memory-limit 256Mi --output json --verbose \
  testdata/rollout/configs/agent.yaml testdata/rollout/configs/provider.yaml \
  testdata/rollout/configs/invalid-reference.yaml > /tmp/rollout-baseline.json
# Expected exit 1: both provider.yaml and invalid-reference.yaml are rejected.
```

## Validation record and exception decisions

Local observation on **2026-10-13**, macOS/arm64: `check.sh` passed against the
candidate and official contrib Collector above. Archive SHA-256:
`6c03308935573712a795b4229f756bc4288bbbb13850604f3c7287868af84d4b`;
extracted Collector SHA-256:
`5caadd3c5f99484b70c2cc204422956942ea8151d6ad1baf5f226b10332b2f19`.
The baseline comparison was also run from archived `v0.1.0` source at the commit
above: agent passed; static invalid reference and the valid provider case failed.
This records configuration validation, not successful startup, delivery,
resource behavior under load, or a production false-positive rate.

| Finding or disagreement | Decision for this observation |
| --- | --- |
| Baseline rejects a valid provider reference | False positive; fixed in the candidate. Require the fix in the adopted release. |
| Candidate accepts a provider reference that resolves to `missing` | Known missed runtime error; keep Collector validation with controlled provider inputs. Never treat static pass as proof of startup validity. |
| `no-persistent-queue` info on normal configs | True statement, retained without suppression. Queue-loss tolerance and durable gateway storage require owner review; no production exception approved. |
| `unused-component` on the static negative control | Expected secondary finding, not a false positive. |
| Memory control fails lint but passes Collector | Expected deployment policy distinction. Confirm real request/limit values before using this rule to block. |

No rules are disabled or downgraded. `minSeverity: info` retains observations;
`failOn: error` only defines the example's verdicts. During observation, retain
nonzero exit codes and full logs as evidence, while keeping the job optional and
independent of deployment. Exit code 2 is an execution/configuration failure,
not a successful observation or an approved policy exception.

## Before a required deployment gate

The **final blocking severity/rule policy is pending maintainer approval**.
This example adds no required check or deployment enforcement.

- Supply sanitized agent/gateway configs and ConfigMaps representative of the
  team's workloads, including actual memory resources, distributions and
  provider behavior. Record source provenance and reviewer approval of sanitization.
- For each config, record expected findings, lint exit, actual Collector
  validation/startup result and every false positive or missed error. Resolve
  known correctness defects affecting that corpus before selecting blockers.
- Pin the adopted linter release/Action commit and verified image, Collector
  artifact and schema snapshot. Re-run this record after any pin or policy change.
- Approve the blocking rules and severities explicitly. For each exception,
  record the config, rule, reason, owner and review/expiry condition; retain
  evidence for findings that do not block.
- Record the approving owner/date and required-check name before enabling the
  deployment gate. Until then, #170's representative-corpus and final-policy
  acceptance criteria remain open.
