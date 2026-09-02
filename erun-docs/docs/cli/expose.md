---
title: erun expose
---

# erun expose

Expose an in-namespace Service at a stable public hostname under the platform's services zone. `erun expose` is for [platform deployments](/concepts/networking#platform-service-exposure) — installations that run the PowerDNS singleton and declare a [`platform:` block](/reference/configuration#platform-block). It does two things: it ensures a **per-environment wildcard DNS record** points at the env's ingress IP, and it applies a **Host-routing Ingress** for the Service.

A [hosted platform](/collaboration/hosted-environments) already runs this for you against its runtime environments' MCP edge as part of their server-side deploy — see [Hosted platform · Automatic exposure](/concepts/hosted-platform#automatic-exposure). Run this command by hand — or use the [desktop app's Ports tab](/desktop/settings-and-ports) — for any other service you want to expose, or on a platform that predates automatic exposure.

```bash
erun expose team dev api --ip 203.0.113.10
erun expose team dev api --ip 203.0.113.10 --port 8080
erun expose team dev api --ip 127.0.0.1 --dry-run     # preview, no side effects
```

## What it does

For `erun expose <tenant> <env> <service>`, `<service>` is the **logical service name**: it becomes the DNS label in the public hostname `<service>.<tenant>-<env>.<servicesZone>` (the services zone comes from the platform config — e.g. `api.team-dev.services.erunpaas.com`) and the Ingress routes it to the tenant-scoped in-namespace Service `<tenant>-<service>` — the name that service's component chart renders (e.g. `api` → `team-api`). The public host stays a clean label while the Ingress targets the real Service.

**When the Service is not named that way.** That derivation is right for a chart erun scaffolded and wrong for a repo that brought its own, whose chart names its Service itself. Routing to a derived name that does not exist produces the worst shape of failure: a hostname that resolves and an ingress that 503s. A caller that already knows the Service — the desktop's picker, which lists the namespace's Services — names it explicitly instead, and the derivation stays the default for everyone else. From the CLI, `erun observe` lists the Services an environment runs, so the name to route to is one read away.

The flow:

1. **Per-env wildcard A record.** It upserts `*.<tenant>-<env>.<servicesZone>` → `--ip` (TTL 60) in the platform's authoritative zone. The wildcard covers *every* service in that env, so exposing additional services later only adds an Ingress — the DNS record is written once. It writes this directly by exec'ing `pdnsutil` inside the platform's PowerDNS pod when it has that cluster access (a hosted deploy Job); with an `erun`-type cloud alias configured instead (`erun cloud init erun`) — the case for a developer's own local cluster, which never has credentials for the platform's cluster — it performs the same write through the platform's API instead. See [Networking spec · Platform service exposure](/agent-reference/networking-spec#platform-service-exposure) for the exact decision.
2. **Host-routing Ingress.** It applies an Ingress named `expose-<service>` into the env's namespace, routing the hostname to the tenant-scoped Service `<tenant>-<service>` on `--port` (default `80`).

The DNS write targets the **platform** environment's cluster (where PowerDNS runs); the Ingress is applied to the **target** env's cluster. These can be different clusters — `expose` resolves each context independently.

HTTPS is requested by default, but it only takes effect when something will actually populate the env's per-env wildcard cert Secret (`<tenant>-<env>-wildcard-tls`): the Ingress carries a `tls:` block referencing it and sets `ingressClassName` only once `--dns01-token-file`, `--dns01-broker-url`, and `--acme-email` are all set, provisioning that Secret through erun's DNS-01 broker — see [Networking spec · Platform service exposure](/agent-reference/networking-spec#platform-service-exposure) for the exact mechanism. On a hosted platform these three flags are supplied automatically as part of the environment's server-side deploy; running `expose` by hand for it needs nothing extra. Without them, `expose` resolves to the same plain `http://` Ingress `--no-tls` asks for explicitly, rather than referencing a Secret nothing will ever populate. Pass `--ingress-class` / `--tls-secret` to override the defaults.

## Removing exposure

[`erun unexpose <tenant> <env>`](/agent-reference/networking-spec#unexposing) removes the per-env wildcard DNS record `expose` created. A hosted platform's environment deletion already runs this for you — see [Hosted platform · Automatic exposure](/concepts/hosted-platform#automatic-exposure). Run it by hand only if you exposed an environment manually and are tearing it down outside the normal delete flow.

## From the desktop

The desktop app covers the same ground without a terminal: an environment's settings → **Ports** tab has a **Public access** section that lists every exposed service's hostname, exposes a new one, and removes public access for the whole environment (the same scope as `erun unexpose` above). See [Desktop app · Settings and ports](/desktop/settings-and-ports).

## Flags

| Flag | Description |
|---|---|
| `--ip <ip>` | **Required.** The env's ingress IP the per-env wildcard record points at — `127.0.0.1` for a VM-backed local cluster, a node/LAN IP, or the public LB IP for a remote cluster. |
| `--port <int>` | Service port the Ingress routes to. Default `80`. |
| `--skip-if-unconfigured` | Succeed as a no-op instead of the "no platform block" error below, for a script that calls `expose` after another command without knowing whether the target project is a platform deployment. |
| `--erun-alias <alias>` | Which configured `erun`-type cloud alias routes the DNS write through the platform's API when direct PowerDNS access is unavailable. Defaults to the sole configured alias; only needed to disambiguate when more than one is configured. |
| `--dry-run` | Resolve and print the full plan — the hostname, the DNS write (`pdnsutil` exec or the platform API call, whichever applies), and the Ingress apply — without touching DNS or the cluster. |

## Error behaviour

| Condition | What happens | Recover |
|---|---|---|
| No `platform:` block (or no base domain) in `.erun/config.yaml` | Aborts: `a platform block with a base domain is required in .erun/config.yaml`; exit 1. | Add a [`platform:` block](/reference/configuration#platform-block). |
| Malformed `platform:` block | Aborts with the specific validation error (bad base domain, services zone not under it, unparseable authoritative IP, …); exit 1. | Fix the offending field. |
| `platform:` block has no `env` | Aborts: `platform.env is required in .erun/config.yaml …`; exit 1. `expose` derives the PowerDNS pod's namespace from `platform.env`, so it cannot run without it. | Set `platform.env` to the platform environment that runs PowerDNS. |
| `--ip` omitted | Aborts: `a target IP is required …`; exit 1. | Pass `--ip <env ingress IP>`. |
| Service name is not a DNS-1035 label | Aborts before any DNS write: `service name "…" must be a DNS-1035 label …`; exit 1. | Use a lowercase letters/digits/hyphen name. |
| `pdnsutil` exec or Ingress apply fails (live run) | Surfaces the underlying `kubectl` error; the wildcard record and the Ingress are applied in that order, so a later failure can leave the DNS record written. | Re-run once the cluster issue is resolved — both operations are idempotent (record replace, Ingress apply). |

## See also

- [Networking · Platform service exposure](/concepts/networking#platform-service-exposure) — the model and when to use it.
- [Networking spec · Platform service exposure](/agent-reference/networking-spec#platform-service-exposure) — the exact records, Ingress, and resolution rules.
- [Configuration · `platform:` block](/reference/configuration#platform-block) — the per-instance platform config.
