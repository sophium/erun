---
title: erun expose
---

# erun expose

Expose an in-namespace Service at a stable public hostname under the platform's services zone. `erun expose` is for [platform deployments](/concepts/networking#platform-service-exposure) — installations that run the PowerDNS singleton and declare a [`platform:` block](/reference/configuration#platform-block). It does two things: it ensures a **per-environment wildcard DNS record** points at the env's ingress IP, and it applies a **Host-routing Ingress** for the Service.

```bash
erun expose team dev api --ip 203.0.113.10
erun expose team dev api --ip 203.0.113.10 --port 8080
erun expose team dev api --ip 127.0.0.1 --dry-run     # preview, no side effects
```

## What it does

For `erun expose <tenant> <env> <service>`, `<service>` is the **logical service name**: it becomes the DNS label in the public hostname `<service>.<tenant>-<env>.<servicesZone>` (the services zone comes from the platform config — e.g. `api.team-dev.services.erunpaas.com`) and the Ingress routes it to the tenant-scoped in-namespace Service `<tenant>-<service>` — the name that service's component chart renders (e.g. `api` → `team-api`). The public host stays a clean label while the Ingress targets the real Service. The flow:

1. **Per-env wildcard A record.** It upserts `*.<tenant>-<env>.<servicesZone>` → `--ip` (TTL 60) in the platform's authoritative zone by exec'ing `pdnsutil` inside the platform's PowerDNS pod. The wildcard covers *every* service in that env, so exposing additional services later only adds an Ingress — the DNS record is written once.
2. **Host-routing Ingress.** It applies an Ingress named `expose-<service>` into the env's namespace, routing the hostname to the tenant-scoped Service `<tenant>-<service>` on `--port` (default `80`).

The DNS write targets the **platform** environment's cluster (where PowerDNS runs); the Ingress is applied to the **target** env's cluster. These can be different clusters — `expose` resolves each context independently.

The exposed URL is **HTTPS** by default: the Ingress references the env's per-env wildcard cert Secret (`<tenant>-<env>-wildcard-tls`, issued once per env by the cluster edge) and sets `ingressClassName`, so the host serves `https://` with no per-service cert step. Pass `--no-tls` for http, `--ingress-class` / `--tls-secret` to override. The cert is issued via the edge's DNS-01 `ClusterIssuer` (Cloudflare, or PowerDNS RFC2136 once the services zone is delegated) — see [Networking spec · Platform service exposure](/agent-reference/networking-spec#platform-service-exposure).

## Flags

| Flag | Description |
|---|---|
| `--ip <ip>` | **Required.** The env's ingress IP the per-env wildcard record points at — `127.0.0.1` for a VM-backed local cluster, a node/LAN IP, or the public LB IP for a remote cluster. |
| `--port <int>` | Service port the Ingress routes to. Default `80`. |
| `--dry-run` | Resolve and print the full plan — the hostname, the `pdnsutil` exec, and the Ingress apply — without touching DNS or the cluster. |

## Error behaviour

| Condition | What happens | Recover |
|---|---|---|
| No `platform:` block (or no base domain) in `.erun/config.yaml` | Aborts: `expose requires a platform block with a base domain in .erun/config.yaml`; exit 1. | Add a [`platform:` block](/reference/configuration#platform-block). |
| Malformed `platform:` block | Aborts with the specific validation error (bad base domain, services zone not under it, unparseable authoritative IP, …); exit 1. | Fix the offending field. |
| `platform:` block has no `env` | Aborts: `expose requires platform.env in .erun/config.yaml …`; exit 1. `expose` derives the PowerDNS pod's namespace from `platform.env`, so it cannot run without it. | Set `platform.env` to the platform environment that runs PowerDNS. |
| `--ip` omitted | Aborts: `a target IP is required …`; exit 1. | Pass `--ip <env ingress IP>`. |
| Service name is not a DNS-1035 label | Aborts before any DNS write: `service name "…" must be a DNS-1035 label …`; exit 1. | Use a lowercase letters/digits/hyphen name. |
| `pdnsutil` exec or Ingress apply fails (live run) | Surfaces the underlying `kubectl` error; the wildcard record and the Ingress are applied in that order, so a later failure can leave the DNS record written. | Re-run once the cluster issue is resolved — both operations are idempotent (record replace, Ingress apply). |

## See also

- [Networking · Platform service exposure](/concepts/networking#platform-service-exposure) — the model and when to use it.
- [Networking spec · Platform service exposure](/agent-reference/networking-spec#platform-service-exposure) — the exact records, Ingress, and resolution rules.
- [Configuration · `platform:` block](/reference/configuration#platform-block) — the per-instance platform config.
