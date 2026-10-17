# Rollout issue verification

Reviewed on 2026-10-18 against main `a7bdab2`. The implementation PRs listed
in #207 have all merged. The earlier tracker text saying none had merged is
outdated. A merged implementation does not establish the original rollout's
binary identity or complete its acceptance evidence.

| Issue | Implementation on main | Verification status |
| --- | --- | --- |
| #208 | #223, per-file rule policy | Merged; covered by the current Go tests |
| #209 | #224, inline suppression | Merged; covered by the current Go tests |
| #210 | #225, credential metadata suffixes | Merged; covered by the current Go tests |
| #211 | #226, connector memory admission | Merged; covered by the current Go tests |
| #212 | #227, connector batching | Merged; covered by the current Go tests |
| #213 | #229, sampled debug output | Merged; covered by the current Go tests |
| #214 | #230, CA and skipped TLS verification | Merged; covered by the current Go tests |
| #215 | #231, HTTP decoder bookkeeping | Generator fix merged; rollout-version and published-schema verification remains open |
| #216 | #232, settings-relative schemas | Merged; covered by the current Go tests |
| #217 | #233, Kubernetes manifest classification | Merged; covered by the current Go tests |
| #218 | #234, candidate discovery regressions | CLI and Action directory tests pass; original PEM inclusion remains unexplained |
| #219 | #235, alpha notices | Merged; covered by the current Go tests |
| #220 | #236, Kubernetes listener context | Merged; host-network severity corrected by this change; telemetry source still unconfirmed |
| #221 | #237, per-rule summary counts | Merged; covered by the current Go tests |

## Corrections in this change

Kubernetes alone previously lowered wildcard listener findings to info,
including pods sharing the node network. `run.kubernetes.hostNetwork` now
records true, false, or unknown per file. Only an explicitly ordinary pod
(`hostNetwork: false`) gets info. Host networking and unknown networking retain
warning, with messages that describe the known network scope. Explicit severity
policy continues to take precedence. See the
[rule documentation](rules/receiver-binds-all-interfaces.md#kubernetes-deployments)
for the compatibility change and settings example.

The settings loader, generated settings schema, path overrides, verbose
environment output and public library environment carry the same value.
Regression tests cover receiver and extension listeners, default/overridden
network contexts, explicit severity, and opting out with `--kubernetes=false`.

## Remaining evidence

- **#215:** identify the actual rollout builder manifest and module versions,
  regenerate the affected distributions with this generator, compare the
  accepted deprecated/new HTTP keys with the matching upstream schema/decoder,
  and deliver the corrected registry files. The synthetic bookkeeping test alone
  is not evidence that consumers have received corrected schemas. Existing
  schemas may also remain cached; use `--no-cache` when verifying a correction.
- **#218:** record the executed binary version/hash, effective arguments and
  wrapper revision. Current CLI directory discovery excludes PEM, CRT, text and
  JSON, including nested files, while preserving uppercase YAML and explicitly
  named files. The Action regression verifies that `directory/*` expands to
  explicit files and therefore includes PEM; it does not prove that the actual
  rollout used that invocation. Preserve the explicit-file exception.
- **#220:** obtain the original telemetry finding's rule, path, message and
  binary/source version. The current wildcard-bind rule walks used receivers and
  enabled extensions, and its telemetry-reader regression confirms it does not
  emit from `service.telemetry.metrics.readers`. That does not invalidate the
  observed finding or establish its source.

#207 remains a tracker until these acceptance checks are complete. #123 is an
automated dependency dashboard, not an outstanding code defect. No issue is
closed by this verification record, and no release or schema publication is
implied.
