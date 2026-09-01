# hardcoded-secret

**Default severity:** `warning` · **Group:** [Security](README.md#security)

The one rule about the file rather than the collector. Configs live in git, and
exporter credentials are written in the same file as everything else. CI is the
last moment before the value reaches a remote.

## What it reports

The rule walks **every declared component** — wired or not, since a credential
in the repository has already been handed over — and reports a scalar whose key
names a credential and whose value is written out in full.

The key names, matched as a case-insensitive substring so `sasl_password` is
covered: `password`, `token`, `api_key`, `secret`, `credential`, `private_key`,
`key_pem`, `access_key`, `passphrase`. A list is matched by the key that named
it, so `api_keys: [AKIA…]` is reported too, and under `headers:` so are
`authorization` and any value opening with `Bearer` or `Basic`.

**It never prints the value**, only the path. Copying the secret into the CI log
is the one thing this rule must not do.

## Example

```yaml
exporters:
  otlp_grpc:
    endpoint: backend:4317
    headers:
      authorization: Bearer a3f1c99d4b7e2085f6c1
```

```console
$ otelcol-config-lint run config.yaml
config.yaml:18:22: warning: "headers.authorization" is a credential written into the config for exporter "otlp_grpc" [hardcoded-secret]
    hint: move it to a secret store and reference it here as an expansion such as ${env:OTLP_TOKEN}, which the collector resolves at startup
    docs: https://opentelemetry.io/docs/security/config-best-practices/
```

The fix upstream recommends — keep sensitive values in a secret store or on an
encrypted filesystem, and pull them in with an expansion:

```yaml
      authorization: Bearer ${env:OTLP_TOKEN}    # not the literal token
```

## What it stays quiet about

False positives are the risk this rule carries, so it gives up findings freely:

- `${env:...}` and `${file:...}` — those are the fix, and say so;
- empty values and placeholders (`changeme`, `<your-token>`, `REPLACE_ME`,
  `none`), which are a config with no credential in it yet, behind an auth
  scheme as much as on their own;
- a boolean, which is a switch rather than a credential whatever the setting is
  called;
- a key naming *where* a credential lives rather than holding one —
  `private_key_file`, `token_url`, `cert_pem`. Keys ending in `_file`, `_path`,
  `_url`, `_uri` and `_name` are excluded before anything else.

It reports at `warning`, because a local config with a dummy credential is
legitimate and this rule will meet plenty of them.

## Docs

- [Config best practices](https://opentelemetry.io/docs/security/config-best-practices/)

## See also

- [`../../testdata/rules/hardcoded-secret.yaml`](../../testdata/rules/hardcoded-secret.yaml)
