---
title: Networking
---

# Networking

How traffic gets into an environment, and how services inside an env talk to each other.

## Three connection paths

ERun never invents a custom protocol for connecting to an env. Every connection is one of three standard paths:

| Path | Used by | Mechanism |
|---|---|---|
| **SSH** to the runtime pod | Operator (terminal, IDE) | The desktop app port-forwards the in-pod SSH server (`EnvConfig.sshd.localport`) to localhost. Any SSH-speaking editor attaches. |
| **MCP** to the runtime pod | Agent (Claude Code, Codex, custom) | Same desktop port-forward, different port — published in `<UserConfigDir>/erun/portforward/mcp/<tenant>/<env>.json`. |
| **HTTP / TCP** to an application service | Browsers, curl, other services | A normal Kubernetes `Service` inside the env's namespace. Exposed via one of the patterns below. |

The first two are stable — every env has them, the desktop manages them. The third is where you make choices.

## Exposing application services

Four patterns, picked by what the env is for. The manifest skeletons for each (Ingress, NetworkPolicy, LoadBalancer Service, hostPort pod spec) are on [Agent reference · Networking spec](/agent-reference/networking-spec).

| Pattern | When to use |
|---|---|
| **`kubectl port-forward`** | Testing alone against the env. No DNS, no TLS — bound to a terminal session. Simplest. |
| **`hostPort`** | Local clusters only (Docker Desktop, OrbStack). Always-on `localhost:<port>` URL; the desktop allocates a non-overlapping port range per env so multiple envs co-exist. |
| **Ingress per env** | Production-ish. Each env gets its own hostname (`<service>.<env>.<domain>`) without per-env chart edits. Requires a cluster-wide Ingress controller + wildcard DNS + wildcard TLS. |
| **LoadBalancer per env** | Cloud envs without a shared Ingress controller. One cloud LB per env — easy to wire, more expensive at scale. |

## Inter-env communication

By default, **envs cannot reach each other.** ERun's runtime chart deploys a default-deny NetworkPolicy on every env's namespace. Cross-env traffic requires an explicit opt-in NetworkPolicy on the target plus a matching label on the consumer namespace; this is rare and usually a sign you should be using one env rather than two. See [Networking spec · Cross-namespace traffic semantics](/agent-reference/networking-spec#cross-namespace-traffic-semantics) for the manifests.

## Per-env DNS

When Pattern 3 (Ingress per env) is in use, each env's services get a hostname of the form `<service>.<tenant>-<env>.<environment>.<domain>` — one wildcard cert covers them all. In-cluster service-to-service traffic uses the standard Kubernetes DNS at `<service>.<tenant>-<env>.svc.cluster.local`. Hostname grammar and the per-segment validation rules: [Networking spec · DNS resolution](/agent-reference/networking-spec#dns-resolution).

## Egress

Outbound traffic from an env is unrestricted by default — pods can reach any external endpoint the cluster's egress allows. To restrict egress, apply a per-env NetworkPolicy with `policyTypes: [Egress]` and an explicit allowlist; see [Networking spec · Egress semantics](/agent-reference/networking-spec#egress-semantics) for the manifest.

## What ERun doesn't manage

To be explicit:

- **Ingress controllers** — install via the cluster's normal mechanism (helm install ingress-nginx, etc.). ERun deploys charts; it doesn't provide a controller.
- **DNS records** — point them at the Ingress LB IP via your DNS provider (Route53, Cloudflare, etc.).
- **TLS certificates** — cert-manager + ACME (Let's Encrypt / ZeroSSL) or a private CA. ERun's runtime pod has no certificate authority of its own.
- **Service mesh** — Istio / Linkerd work alongside ERun. The runtime pod doesn't require a sidecar; if you add one, your application services get it through normal helm chart conventions.

## Quick reference

| Want to | Pattern |
|---|---|
| Hit my service from my browser, right now | `kubectl port-forward` |
| Always-on local URL, multiple envs side by side | `hostPort` + `EnvConfig.localportrangestart` |
| Production-style env URLs (`api.feature-a.dev.myorg.example`) | Ingress per env + wildcard DNS + wildcard cert |
| Quick one-off cloud env, no shared ingress | `type: LoadBalancer` |
| Service in env A talks to env B | Don't. If you must, NetworkPolicy + namespace label. |
