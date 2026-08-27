---
title: Metrics spec
---

# Metrics spec

> For the Operator view, see [Observability](/concepts/observability).

ERun's runtime pod exposes a Prometheus-format metrics endpoint. The full schema — endpoint, every metric name, every label, every type — is below. Application services declared in `.erun/config.yaml` use their own framework's metrics conventions; ERun has no opinion on those.

## Endpoint

| Property | Value |
|---|---|
| Listener | `erun-devops` container, port `9100`, bound `0.0.0.0` (not loopback — a cluster Prometheus reaches it over the pod network). |
| Path | `/metrics`. |
| Format | Prometheus text exposition format, v0.0.4. |
| Authentication | None — the listener carries no token or mTLS check, matching Prometheus scrape convention. Reachability is instead scoped by the runtime chart's `NetworkPolicy`: always open to pods in the same namespace, and open to a pod in another namespace only when that namespace carries the label `network-policy/erun-metrics-scraper: "true"`. `ssh` and `mcp` keep their prior, fully unrestricted reachability — the policy exists solely to scope the new metrics port, not to lock down ports that were open before it existed. Set `metricsEnabled=false` on the chart to remove the listener (and its `NetworkPolicy`) entirely. |
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
      regex: erun-devops
      action: keep
    - source_labels: [__meta_kubernetes_namespace]
      target_label: namespace
    - source_labels: [__meta_kubernetes_pod_name]
      target_label: pod
```

For Prometheus running outside the target namespace (the usual case — one cluster-wide Prometheus scraping every tenant's runtime pods), label Prometheus's own namespace once: `kubectl label namespace <prometheus-namespace> network-policy/erun-metrics-scraper=true`. Without that label, the scrape config above resolves targets fine (namespace listing is a Kubernetes API concern, not a network one) but the actual scrape connection is refused by the target pod's `NetworkPolicy`.

## Metrics

Every series is labelled with the env's tenant + environment, derived from `ERUN_TENANT` and `ERUN_ENVIRONMENT` at process start. Additional per-metric labels are listed below.

### `erun_idle_eligibility`

| Property | Value |
|---|---|
| Type | `gauge` |
| Labels | `tenant`, `environment` |
| Range | `0` (env not eligible for idle-stop) or `1` (eligible). |
| Source | `emcp` runs its own 10-second ticker that calls the same read-only idle-status resolution the `idle` MCP tool exposes (`eruncommon.ResolveStoredEnvironmentIdleStatus`; see [Idle policy](/agent-reference/idle-policy)), so this gauge can never disagree with what `idle` reports. Sampled once immediately at boot as well, so a scrape right after pod start does not read a stale zero-value gauge as "eligible". |
| Reset on | Process restart. |

### `erun_terminal_input_seconds_since_last`

| Property | Value |
|---|---|
| Type | `gauge` |
| Labels | `tenant`, `environment` |
| Unit | seconds. |
| Source | Wall-clock now minus the latest of the `ssh`, `cli`, and `mcp` activity kinds' last-recorded timestamps — the same union [Idle policy · `last_terminal_input`](/agent-reference/idle-policy#last_terminal_input) already specifies (a keystroke, a successful in-pod `erun` invocation, or a non-idle-probe MCP call), read from the same on-disk activity snapshots the `idle` MCP tool exposes rather than a metrics-only redefinition. Sampled on the same 10-second ticker as `erun_idle_eligibility`. |
| Initial value | `0` immediately after pod start (no activity of any of the three kinds observed yet). |

### `erun_traffic_window_bytes`

| Property | Value |
|---|---|
| Type | `gauge` |
| Labels | `tenant`, `environment` |
| Unit | bytes. |
| Source | The delta between two samples taken 60 seconds apart, not a fixed byte counter of any single socket: MCP bytes are counted directly in `emcp`'s own HTTP middleware (request `Content-Length` plus response bytes written); SSH bytes are read from the ssh-proxy's cumulative activity-snapshot counter (`erun-common/activity.go`) and diffed the same way. |
| Window definition | Tumbling 60-second window, sampled on a fixed in-process ticker independent of scrape timing. Each scrape returns the last *completed* window's byte count, not the one still accumulating — a scrape landing early in an otherwise-busy window can therefore read `0` even though traffic flowed a few seconds earlier; the next tick picks it up. |

### `erun_mcp_calls_total`

| Property | Value |
|---|---|
| Type | `counter` |
| Labels | `tenant`, `environment`, `tool`, `result` |
| `tool` values | The MCP tool name as registered in `erun-mcp/server.go` — see [MCP overview](/mcp/overview) for the current list rather than duplicating it here; a prior version of this page listed 13 example names, several of which (`logs`, `open`) do not exist as tool names and several of which (most of the surface) it omitted. |
| `result` values | `success`, `error`, or `dry_run`. Derived from `guardTool`, the one dispatch point every tool call passes through: an error return is `error`; absent an error, a tool whose result carries the shared `CommandOutput.Executed` field (the delivery tools — `build`, `push`, `deploy`, `doctor`, `pin`, `terraform`, and the rest) reports `dry_run` when `Executed` is false; every other tool (read-only tools with no preview concept, e.g. `idle`, `list`, `version`, `usage`) reports `success` whenever it returns no error. |
| Reset on | Process restart. (Counters are monotonic within a process.) |

### `erun_audit_events_total`

| Property | Value |
|---|---|
| Type | `counter` |
| Labels | `tenant`, `environment`, `action`, `actor_kind`, `result` |
| `action` values | Today, only the `mcp.<tool>` namespace (the same dispatch point `erun_mcp_calls_total` reads, `guardTool`) — one increment per call, both allowed and denied. The `erun.<command>` and `api.<resource>.<verb>` namespaces [Audit log format](/agent-reference/audit-log) describes are **not yet wired into this counter**: the CLI has no in-pod audit writer of its own yet, and the durable, API-side `audit_events` table has no CLI/MCP caller yet either (see that page's "(Planned.)" note) — this counter is a separate, lighter-weight in-process tally of the MCP edge's own authorization decisions, not a read of that table. |
| `actor_kind` values | `agent` for every sample today, because this counter's only source (the MCP edge) is Agent-only traffic by construction — an Operator's actions go through the CLI, which this counter does not read from yet. `operator` is reserved for when a CLI audit source is wired in. |
| `result` values | `success`, `error` (including a capability refusal), or `dry_run` — same derivation as `erun_mcp_calls_total`, except a refusal (the call never reaches the tool handler) also counts here as `error`, since it is still an audited decision. |

## Cardinality envelope

The label sets above are bounded:

| Labels | Cardinality bound |
|---|---|
| `tenant × environment` | One per running env. |
| `tool` | ≤ 100 (the MCP-registered tool set, ~90 today including one-release deprecated aliases — see `erun-mcp/server_test.go`'s `wantRegisteredTools` for the exact, tested count). |
| `result` | 3 (`success`, `error`, `dry_run`). |
| `action` | Same bound as `tool` today (only the `mcp.<tool>` namespace is wired in); will grow once `erun.<command>` and `api.<resource>.<verb>` are wired. |
| `actor_kind` | 1 today (`agent` only); 2 (`operator`, `agent`) once a CLI audit source is wired in. |

A single-env Prometheus deployment emits at most ~600 series from this pod today: 3 gauges, `erun_mcp_calls_total` at ≤300 (`tool` × `result`), and `erun_audit_events_total` at ≤300 (`action` × `actor_kind` × `result`, currently 1 `actor_kind` value). Both counters' bounds double once the `erun.<command>` and `api.<resource>.<verb>` action namespaces and the `operator` actor kind are wired in — comfortably inside Prometheus's per-target defaults either way.

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
- [Audit log format](/agent-reference/audit-log) — the durable, API-side audit trail and its `<namespace>.<verb>` action-verb convention; `erun_audit_events_total` follows the same convention but is a separate, in-process count, not a read of that table (see the note on its `action` values above).
