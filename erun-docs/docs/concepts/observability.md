---
title: Observability
---

# Observability

What can you see when something goes wrong (or right). ERun doesn't ship a built-in observability stack — Kubernetes is the substrate, and you reach for the cluster's normal tools.

## Three layers

| Layer | What you see | How to read it |
|---|---|---|
| **In-pod** | Stdout/stderr of every container, plus files under `/var/log/erun/` (CLI audit traces). | `kubectl logs`, `erun open` + shell, MCP `raw`. |
| **Cluster** | Aggregated logs / metrics / traces across all envs on the cluster. | Whatever stack you've installed — Prometheus + Grafana + Loki, OpenTelemetry Collector, Datadog, …. |
| **Durable** | Reviews, comments, builds, audit events — anything posted to the erun API. | erun API endpoints (admin-only for audit events; see [Audit log · Security events](/agent-reference/audit-log#security-events)). |

The first layer is always there. The other two are admin-opt-in.

## Logs

### From your laptop

```bash
# Application service logs:
kubectl logs -n <tenant>-<env> -l app=<component> --tail=200 -f

# Runtime pod (Operator/Agent shared surface):
kubectl logs -n <tenant>-<env> -c erun-devops <runtime-pod> --tail=200

# All containers in a pod:
kubectl logs -n <tenant>-<env> <pod> --all-containers --prefix
```

The runtime pod has two containers (`erun-devops`, `erun-dind`); `-c <container>` selects between them.

### From inside the env

After `erun open`, you're in `erun-devops`. The CLI's audit trace lives at `/var/log/erun/audit.log` — JSON-lines, [event shape](/agent-reference/audit-log#event-shape) documented separately.

```bash
tail -F /var/log/erun/audit.log | jq -r '.action + " " + .result'
```

### Cluster-wide aggregation

ERun doesn't deploy a log aggregator. The conventional setup is **Loki + Promtail** (or Vector / Fluent Bit / your existing aggregator) installed once per cluster. Every env's containers ship their stdout/stderr through it automatically.

In Grafana, filter by the Kubernetes namespace label to scope to one env: `{namespace="<tenant>-<env>"}`.

## Is this environment busy?

Logs tell you what happened; this tells you whether anything is happening right now. An environment reports its own condition, and the desktop's sidebar renders it — so an environment being driven hard by an Agent looks different from one nobody has touched in a week, and different again from one that is stopped.

Three things feed it:

- **Activity leases.** Long work announces itself for its lifetime (`erun activity lease take --name <what>`, or the MCP `activity_lease_take` tool). This is the only signal that names the work, and the only one that survives a job making no calls for forty minutes. Idle-stop will not stop an environment holding one.
- **Sampled processes.** The in-pod monitor notices resident build and agent processes that are actually burning CPU, so work nobody instrumented still registers.
- **Request activity.** SSH, API, MCP, and CLI traffic, as before.

Read it from anywhere with the MCP `idle` tool or `erun idle <tenant> <env> --json`: `leases` says what is holding the environment, `markers` breaks the rest down per source, and `stopBlockedReason` names whichever one is deferring auto-stop.

For the full schema and the expiry / liveness rules, see [Agent reference · Idle policy](/agent-reference/idle-policy#activity-leases); for the operator-facing commands, [`erun idle` · Activity leases](/cli/idle#activity-leases).

## Metrics

ERun's runtime pod exposes a single Prometheus-format metrics endpoint on port `9100`. The series cover idle-eligibility, terminal-input freshness, the network-traffic window, MCP tool-call counts, and audit-event counts — labelled by tenant + environment.

For the full schema (every metric name, every label, type, source, cardinality envelope), see [Agent reference · Metrics spec](/agent-reference/metrics-spec).

Application services use the metrics conventions of their own framework. ERun has no opinion — scrape with the cluster's normal Prometheus setup.

## Traces

For distributed traces across the env's services, deploy an OpenTelemetry Collector as a sidecar to your services (or run one as a DaemonSet). ERun has no built-in trace pipeline; the env's namespace is just a Kubernetes namespace, so any standard tracing setup works inside it.

The runtime pod itself does not emit traces. The audit log + MCP tool counts are the closest equivalent — they capture every Operator and Agent action with timestamps.

## The audit trail

Distinct from logs / metrics / traces, the [audit trail](/collaboration/operator-in-the-loop#the-audit-trail) is ERun's primary record of *who did what when*. Three storage layers:

- In-environment trace — `/var/log/erun/audit.log` in each runtime pod (lifetime of the pod).
- MCP events — 30 days inside the pod.
- erun API events — durable, lives as long as the tenant.

When a service incident needs an "actor and intent" reconstruction, the audit trail is the source of truth. Logs and metrics are for "what was the state of the system."

## Quick reference

| You need | Where to look |
|---|---|
| Service crashed | `kubectl logs -n <tenant>-<env> -l app=<component> --previous` |
| Idle-stop fired unexpectedly | MCP `idle` tool result + `erun_idle_*` Prometheus series |
| Is anything running in this env right now | Sidebar row indicator; MCP `idle` tool `leases` + `markers` |
| Why an idle-looking env never stops | `stopBlockedReason` in the MCP `idle` tool result — a held lease names itself |
| An Agent's recent actions | `/var/log/erun/audit.log` or the API audit-events table |
| A merge happened — who advanced the queue | Security events: `mergequeue.advance` |
| The runtime pod restarted | `kubectl describe pod -n <tenant>-<env> <pod>` (Kubernetes events) |
| Trace request across services | OpenTelemetry collector you installed; ERun adds nothing here |

## What ERun doesn't ship

To be explicit:

- No log aggregator (Loki / Fluentd / Vector — install once per cluster).
- No metrics database (Prometheus / VictoriaMetrics — install once per cluster).
- No trace pipeline (OTel Collector — install per service as needed).
- No dashboards (Grafana / Kibana / Datadog — install once per cluster).

The cluster owns the observability stack. ERun owns the **audit trail** — that's the one piece it does ship, because it's tied to the platform's identity and trust model.
