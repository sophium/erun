---
title: erun services
---

# `erun services`

List the Services an environment's namespace runs, with each one's type, ports, and the public address it already has. Read-only — both reads are `kubectl get`s, never anything that can create, change, or delete a cluster object, which is what makes it safe to grant to an orchestrator that must never be handed [`erun exec raw`](/cli/exec).

It answers the question that precedes [`erun expose`](/cli/expose): **what is this environment running, and which of it is already published.** It is the same read the desktop app's Ports tab renders as its Service picker.

## Synopsis

```
erun services [--tenant <t>] [--environment <e>] [flags]
```

## What it shows

```bash
erun services --tenant team --environment dev
```

```
Namespace: team-dev
Services (3):
  team-api (ClusterIP): ports http:80
    exposed as api at https://api.team-dev.services.erunpaas.com
  pw-api (ClusterIP): ports http:80
  team-mcp (ClusterIP): ports mcp:80
```

Each Service carries the port an Ingress would route to. A Service already fronted by an `erun expose` Ingress carries the public address it has on an indented line beneath it, named by its public label — which is not necessarily the Service's own name.

The exposure is read from the **Ingress's own backend**, not re-derived from the `<tenant>-<service>` convention. That matters for exactly the case [`erun expose`](/cli/expose) exists to handle: a repo that brought its own chart names its Service itself, and re-deriving the name here would report a Service that does not exist rather than the one the Ingress actually routes to. Pass `--backend-service` to `expose` with that name.

Because the listing is a `kubectl get`, it reports the namespace's real state rather than any erun-owned record: a Service nothing has exposed reads the same whether the environment has never been exposed or its Ingresses were removed by hand.

## Flags

| Flag | Description |
|---|---|
| `--tenant`, `--environment` | Target a specific tenant/environment; default to the current scope. |
| `--output json` | Emit the full result as JSON (`tenant`, `environment`, `namespace`, and the `services` array). |
| `--dry-run` | Trace the two `kubectl get` calls that would run without executing them. |

The structured result's exact shape is specified in [Agent reference · `erun services`](/agent-reference/cli-flags#erun-services).

## From an MCP-connected orchestrator

The same read reaches an Agent through the `services` MCP tool — see [MCP overview § Inspection](/mcp/overview#inspection--read-only). An agent that can act on an environment but cannot see what it runs has to be told the service name out of band, which is the gap this closes.

## Error behaviour

| Failure | Behaviour |
|---|---|
| Tenant/environment can't be resolved. | Errors before any `kubectl` call. |
| The namespace or cluster is unreachable. | Errors naming the failed `kubectl get`. |
| The credentials cannot list Services in the namespace. | Errors, distinguishing the authorization failure from a namespace that is genuinely empty. |

## See also

- [`erun expose`](/cli/expose) — publish one of these at a public hostname.
- [`erun observe`](/cli/observe) — the whole-namespace view, of which this is the Service-and-exposure part.
