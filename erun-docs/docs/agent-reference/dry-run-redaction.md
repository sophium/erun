---
title: Dry-run redaction
---

# Dry-run redaction

> For the Operator view, see [CLI overview](/cli/overview).

Every `erun` command supports `--dry-run`, and the resulting trace is byte-for-byte identical to a real-run trace except that values matching one of the redaction patterns below are replaced with a `<redacted…>` placeholder. The same redaction is applied to real-run traces — a real `docker build` invocation is logged with the same flag set you'd see in `--dry-run`; only the matched values are rewritten. A real run can additionally log recovery decisions for events a preview cannot foresee (for example reusing an AWS resource that already exists); those lines appear only when the event occurs.

The contract: previews are safe to paste into a PR description or chat without leaking secrets.

## Redaction patterns

Redaction runs against every line emitted to the audit / trace stream. Each pattern below is evaluated in declaration order against the value about to be logged; the **first** match wins (a value cannot be redacted twice). The placeholder identifies which class of secret was detected.

| # | Pattern | Matches | Placeholder |
|---|---|---|---|
| 1 | `(?i).*(TOKEN\|SECRET\|PASSWORD\|KEY\|CREDENTIAL).*` (env var **name**) | The *value* of any env var whose key contains one of these tokens (case-insensitive). | `<redacted>` |
| 2 | Field-source flag `sensitive: true` on `EnvConfig` / `ProjectConfig` / `TenantConfig` | Values sourced from any field tagged sensitive in the configuration schema. | `<redacted>` |
| 3 | HTTP request header `Authorization: Bearer <...>` (case-insensitive header name) | The token portion after `Bearer ` in any HTTP request logged via the http-trace adapter. | `Authorization: Bearer <redacted>` |
| 4 | `eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+` | Anything that looks like a JWT. | `<redacted-jwt>` |
| 5 | `AKIA[0-9A-Z]{16}` | AWS-style access keys. The accompanying secret-key candidate (a 40-character base64-ish string within the same log line) is redacted using the same placeholder. | `<redacted-aws-key>` |
| 6 | `ghp_[A-Za-z0-9]{36,}` | GitHub personal-access tokens. | `<redacted-github-token>` |
| 7 | `ghs_[A-Za-z0-9]{36,}` | GitHub server tokens. | `<redacted-github-token>` |
| 8 | File-contents loader on `~/.docker/config.json`, `~/.kube/config` (`users[].user.token` field), `~/.ssh/id_*` private-key files | Any contents the loader emits from these files. | `<redacted>` |
| 9 | Argv flag name matching `(?i)^-*(password\|passwd\|secret\|token\|apikey\|api-key\|access-key\|private-key)$` (dashes trimmed before matching) | The next argv element after the flag (`--token X` → `X` redacted), or the value half of a `--flag=value` pair. Applies to any traced or self-echoed command argv — `erun exec raw`, `erun cloud init cloudflare`'s `--api-token`/`--token-name`, and `erun cloud context`'s `kubectl config set-credentials <ctx> --token <adminToken>`. | `<redacted>` |

Patterns 1, 2, and 8 fire on the **source** of the value (env-var name, field tag, file path); patterns 3–7 fire on the **content** of the value itself.

## What is *not* redacted

To keep traces useful, the following are emitted as-is:

- Flag *names* and non-secret flag *values* — `--platform linux/amd64,linux/arm64`, `--tenant my-tenant`.
- File paths (build context, Dockerfile, VERSION sources).
- Image tags, commit hashes, version numbers, environment names.
- Public registry hostnames (`ghcr.io/sophium`, `<acct>.dkr.ecr.<region>.amazonaws.com`).
- Stack traces, helm template diffs, kubernetes events.

A `--build-arg KEY=value` invocation is logged with the same `KEY=value` argument pairs you'd see in a real run; only `value` is rewritten when `KEY` matches pattern 1.

## When redaction happens

Redaction is applied at **log-emit time**, not at command-resolve time. The actual execution still uses the real value — `docker build` receives the real `--build-arg`, `helm upgrade` receives the real values file, `git push` receives the real credentials. Only the trace string emitted to stdout (and to the audit log) is rewritten.

This means:

- Dry-run and real-run traces match byte-for-byte (modulo the final `result:` line and event-driven recovery lines a preview cannot foresee). An Operator reviewing a dry-run sees exactly what the live trace will look like.
- A secret that is never logged is never redacted because there is nothing to rewrite. Redaction is a defence against accidental logging; it is not a substitute for not handing the value to an untrusted subprocess.

## Conflict semantics

Two cases worth specifying:

1. **A value matches multiple patterns.** First match in declaration order wins; the placeholder identifies that class. Example: a string that happens to satisfy both pattern 1 (env-var-named-TOKEN) and pattern 4 (JWT shape) is logged as `<redacted>`, not `<redacted-jwt>`.
2. **A non-secret value coincidentally matches a content pattern.** Example: a deploy plan that includes a literal string starting with `AKIA…` for a non-AWS reason is redacted as `<redacted-aws-key>`. The contract favours false-positive redaction over false-negative leakage; if this bites a real flow, raise an issue and propose a stricter pattern.

## Operator-facing summary

The companion Operator page summarises the policy in one paragraph and refers callers here for the exact regex set: see [CLI overview · `--dry-run`](/cli/overview#dry-run-and-verbosity).

## See also

- [CLI overview](/cli/overview) — Operator-facing summary and verbosity flags.
- [Audit log format](/agent-reference/audit-log) — every redacted trace line lands in the audit log with the redacted form.
