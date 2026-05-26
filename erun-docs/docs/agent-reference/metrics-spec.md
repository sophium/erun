---
title: Metrics spec
---

# Metrics spec

> For the Operator view, see [Observability](/concepts/observability).

ERun's runtime pod exposes a Prometheus-format metrics endpoint. The full schema — endpoint, every metric name, every label, every type — is below. Application services declared in `.erun/config.yaml` use their own framework's metrics conventions; ERun has no opinion on those.

## Endpoint

| Property | Value |
|---|---|
| Listener | `erun-mcp` container, port `9100`. |
| Path | `/metrics`. |
| Format | Prometheus text exposition format, v0.0.4. |
| Authentication | None (loopback-only by default; cross-namespace ingress denied by the runtime chart's `NetworkPolicy`). |
| Scrape cadence | Set by the caller (Prometheus). The runtime pod does not enforce a minimum. |

A typical Prometheus scrape configuration:

```yaml
- job_name: erun-runtime-pod
  kubernetes_sd_configs:
    - role: pod
      namespaces:
        names: []                     # all namespaces
  relabel_configs:
    - source_labels: [__meta_kubernetes_pod_container_name]
      regex: erun-mcp
      action: keep
    - source_labels: [__meta_kubernetes_namespace]
      target_label: namespace
    - source_labels: [__meta_kubernetes_pod_name]
      target_label: pod
```

## Metrics

Every series is labelled with the env's tenant + environment, derived from `ERUN_TENANT` and `ERUN_ENVIRONMENT` at process start. Additional per-metric labels are listed below.

### `erun_idle_eligibility`

| Property | Value |
|---|---|
| Type | `gauge` |
| Labels | `tenant`, `environment` |
| Range | `0` (env not eligible for idle-stop) or `1` (eligible). |
| Source | Recomputed on every idle-monitor tick (see [Idle policy](/agent-reference/idle-policy)). |
| Reset on | Process restart. |

### `erun_terminal_input_seconds_since_last`

| Property | Value |
|---|---|
| Type | `gauge` |
| Labels | `tenant`, `environment` |
| Unit | seconds. |
| Source | Wall-clock now − `last_terminal_input`. Updated on every idle-monitor tick. |
| Initial value | `0` immediately after pod start (no terminal-input event observed). |

### `erun_traffic_window_bytes`

| Property | Value |
|---|---|
| Type | `gauge` |
| Labels | `tenant`, `environment` |
| Unit | bytes. |
| Source | The current rolling-60-second window's byte count, observed at the SSH + MCP sockets. |
| Window definition | Tumbling 60-second window: each scrape returns the window the env is currently inside. |

### `erun_mcp_calls_total`

| Property | Value |
|---|---|
| Type | `counter` |
| Labels | `tenant`, `environment`, `tool`, `result` |
| `tool` values | The string passed as `tools/call.name` — one of `idle`, `doctor`, `list`, `version`, `logs`, `build`, `push`, `deploy`, `release`, `open`, `init`, `delete`, `scaffold`, `regenerate-chart`, `migrate-deps`, `extract-component`, `add-ingress`, `raw`. |
| `result` values | `success`, `error`, `dry_run`. |
| Reset on | Process restart. (Counters are monotonic within a process.) |

### `erun_audit_events_total`

| Property | Value |
|---|---|
| Type | `counter` |
| Labels | `tenant`, `environment`, `action`, `actor_kind`, `result` |
| `action` values | The dotted action verb from the audit log: `erun.<command>`, `mcp.<tool>`, `api.<resource>.<verb>`. See [Audit log format · Action verbs](/agent-reference/audit-log). |
| `actor_kind` values | `operator`, `agent`. |
| `result` values | `success`, `error`, `dry_run`. |

## Cardinality envelope

The label sets above are bounded:

| Labels | Cardinality bound |
|---|---|
| `tenant × environment` | One per running env. |
| `tool` | ≤ 20 (the MCP-registered tool set). |
| `result` | 3 (`success`, `error`, `dry_run`). |
| `action` | Bounded by the registered CLI command, MCP tool, and API endpoint counts (≤ ~60 today). |
| `actor_kind` | 2 (`operator`, `agent`). |

A single-env Prometheus deployment emits ≤ ~400 series from this pod.

## Conformance

The runtime pod's metrics emitter conforms to:

- Prometheus text exposition format v0.0.4.
- OpenMetrics 1.0 is **not** declared (no `# TYPE` lines beyond gauge/counter; no `# UNIT` lines).
- `_total` suffix convention is used on counters.

## Application-service metrics

The metrics above describe the **runtime pod** only. Application services declared in `.erun/config.yaml` use whatever metrics convention their framework ships with (Go `prometheus/client_golang`, Node `prom-client`, Java Micrometer, etc.). ERun's helm chart conventions do not standardise this; consult the cluster's Prometheus configuration for how application pods are scraped.

## See also

- [Observability](/concepts/observability) — Operator-facing summary.
- [Idle policy](/agent-reference/idle-policy) — the predicate that drives `erun_idle_eligibility`.
- [Audit log format](/agent-reference/audit-log) — the underlying source of `erun_audit_events_total`.
