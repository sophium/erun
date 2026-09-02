---
title: Networking spec
---

# Networking spec

> For the Operator overview, see [Networking](/concepts/networking).

The full manifest skeletons, DNS resolution rules, and NetworkPolicy semantics ERun expects. Each pattern below is a copy-pasteable Kubernetes manifest; per-tenant projects customise the placeholders (`<tenant>`, `<env>`, `<service>`, `<port>`, `<domain>`).

## Pattern 1 — `kubectl port-forward`

No manifest. The control surface is the user's shell.

```bash
kubectl port-forward -n <tenant>-<env> svc/<service> <localPort>:<port>
```

Lifetime: bound to the calling process. No reconnection on cluster-side restart.

`<localPort>` is per-env (from `EnvConfig.localportrangestart`) so concurrent forwards for different envs don't collide on the laptop. `<port>` is the **in-cluster** port, which differs by channel: the tenant's `<tenant>-api` service (`erun-api` for the `erun` tenant) is a standalone component chart published on the canonical `APIServicePort` (`17033`) in **every** namespace, so its forward maps `<perEnvLocalApi>:17033`; MCP and SSH forward to the runtime pod, which is deployed on the env's per-env ports, so those map the same per-env number on both sides. Using the per-env number for the API's remote side would target a port the service never exposes.

## Pattern 2 — `hostPort` (local clusters only)

For local Kubernetes (Docker Desktop, OrbStack, k3d), set `hostPort` on the container spec. The Kubernetes node binds the port on the host network namespace, so `localhost:<hostPort>` reaches the pod.

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: <service>
  namespace: <tenant>-<env>
spec:
  replicas: 1
  selector:
    matchLabels: { app: <service> }
  template:
    metadata:
      labels: { app: <service> }
    spec:
      containers:
        - name: <service>
          image: <registry>/<service>:<version>
          ports:
            - containerPort: <port>
              hostPort: <hostPort>          # only on local clusters
```

Constraint: `<hostPort>` must be unique across every pod scheduled to the same node. ERun's per-env port allocator (driven by `EnvConfig.localportrangestart`) reserves a non-overlapping range per env so multiple envs on one node do not collide.

Not portable to cloud — the cluster node's host network namespace is not reachable from the laptop.

## Pattern 3 — Ingress per env (cloud-shared)

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: <service>
  namespace: <tenant>-<env>
  annotations:
    cert-manager.io/cluster-issuer: letsencrypt
spec:
  ingressClassName: <ingress-class>   # your controller's class: nginx, traefik, istio, alb, …
  rules:
    - host: <service>.{{ .Release.Namespace }}.<environment>.<domain>
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: <service>
                port:
                  number: <port>
  tls:
    - hosts:
        - <service>.{{ .Release.Namespace }}.<environment>.<domain>
      secretName: <service>-tls
```

`{{ .Release.Namespace }}` resolves to `<tenant>-<env>` at helm-render time, so each env's Ingress emits a unique hostname without per-env chart edits.

### Required cluster infrastructure for Pattern 3

1. **Ingress controller installed cluster-wide.** NGINX (`ingressClassName: nginx`), Traefik, Istio Gateway, or AWS ALB Controller. ERun does not deploy the controller.
2. **Wildcard DNS record** for `*.<environment>.<domain>` pointing at the controller's LB IP.
3. **Wildcard TLS certificate** (cert-manager + ACME, or a private CA) covering the same wildcard.

## Pattern 4 — LoadBalancer per env

```yaml
apiVersion: v1
kind: Service
metadata:
  name: <service>
  namespace: <tenant>-<env>
spec:
  type: LoadBalancer
  selector: { app: <service> }
  ports:
    - port: 443
      targetPort: <port>
```

Cost note: one cloud-provider load balancer per env. Suitable for one-off cloud envs; not for steady-state.

## DNS resolution

| Hostname template | Resolves to | Notes |
|---|---|---|
| `<service>.<tenant>-<env>.<environment>.<domain>` | The env's Ingress (Pattern 3). | One wildcard cert covers all envs. |
| `<service>.<tenant>-<env>.svc.cluster.local` | In-cluster pod IP, no public DNS. | Standard Kubernetes service DNS. |
| `<service>.<tenant>-<env>` | In-cluster, abbreviated (Kubernetes resolver completes `.svc.cluster.local`). | Works inside pods of any namespace. |

### Hostname grammar

For the public form, each segment must match:

| Segment | Regex |
|---|---|
| `<service>` | `[a-z][a-z0-9-]*` (the component-name rule the language skills teach; see [Skills](/concepts/skills)) |
| `<tenant>-<env>` | `[a-z][a-z0-9-]*-[a-z][a-z0-9-]*` (the namespace label) |
| `<environment>` | `[a-z][a-z0-9-]*` (the cluster's environment alias, e.g. `dev` / `staging` / `prod`) |
| `<domain>` | RFC-1035 domain, set by the cluster admin. |

## Platform service exposure

`erun expose <tenant> <env> <service>` (CLI and the `expose` MCP tool) automates Pattern 3 for a platform deployment — an install that runs the `erun-powerdns` singleton and declares a [`platform:` block](/reference/configuration#platform-block). For the Operator view see [Networking · Platform service exposure](/concepts/networking#platform-service-exposure).

**Inputs.** `tenant`, `env`, `service` (positional — the **logical** service name: the hostname label, resolved to the tenant-scoped backend Service `<tenant>-<service>`); `--ip` (the env's ingress IP the wildcard record points at; required); `--port` (Service port, default `80`); `--skip-if-unconfigured` (succeed as a no-op instead of failing when the project declares no `platform:` block at all — for a caller composing `expose` after another command without knowing in advance whether the target is a platform deployment; see [Hosted platform · Automatic exposure](/concepts/hosted-platform#automatic-exposure) for its one caller today); `--dns01-token-file`/`--dns01-broker-url`/`--acme-email`/`--acme-server`/`--dns01-webhook-group-name` (per-env TLS certificate provisioning through the DNS-01 broker — see **TLS** below); `--dry-run`.

**Resolved plan.**

| Field | Value |
|---|---|
| Public hostname | `<service>.<tenant>-<env>.<servicesZone>` |
| Backend Service | `<tenant>-<service>` (the name the service's component chart renders, e.g. `api` → `frs-api`) |
| Per-env wildcard record | `*.<tenant>-<env>.<servicesZone>` `A` `<ip>`, TTL `60` |
| Services zone | `platform.serviceszone` (defaults to `services.<platform.basedomain>`) |
| Ingress | `expose-<service>` in namespace `<tenant>-<env>`, Host-routing the hostname to `<tenant>-<service>:<port>` |

For `service = mcp`, the backend Service `<tenant>-mcp` is not application-specific — the `erun-devops` runtime chart itself renders it (gated on `mcpEnabled`, the default), fronting the runtime pod's named `mcp` container port on port `80`. Every other `service` value depends on that service's own component chart having rendered `<tenant>-<service>`.

**Execution.** Two side effects, in order:

1. **DNS write.** `kubectl [--context <platform-ctx>] -n <platform-namespace> exec deploy/<platform-tenant>-powerdns -- pdnsutil --config-dir=/etc/pdns-shared replace-rrset <zone> <rel-name> A 60 <ip>`. The PowerDNS Deployment is tenant-scoped (`<platform-tenant>-powerdns`, e.g. `erun-powerdns` for the `erun` tenant). The platform namespace is `platform.env` normalised to a namespace label; the **platform** env's own kube context and tenant are resolved from `platform.env` (a `<tenant>-<env>` label — tenant names carry no hyphen, so the first hyphen splits it). This is distinct from the target env's context — the DNS write lands on the cluster PowerDNS runs on, the Ingress on the target env's cluster, which may differ. If the platform env config is not loadable, the platform context is empty and `kubectl` falls back to the current context.
2. **Ingress apply.** `kubectl [--context <env-ctx>] -n <tenant>-<env> apply -f -` with a `networking.k8s.io/v1` Ingress (`app.kubernetes.io/managed-by: erun-expose`), manifest piped on stdin.

The dry-run trace prints both `kubectl` commands verbatim (including the TTL) plus the resolved hostname, wildcard, and platform namespace — no synthetic verbs, no side effects.

**Records, not the HTTP API.** Writes go through `pdnsutil` against the gpgsql backend (the PowerDNS pod reads its connection — including the postgres password — from a generated `--config-dir` config, so no secret appears in the exec argv). The PowerDNS HTTP API is bound to loopback only and is not used by `expose`.

**TLS.** `https` is requested by default, but the Ingress only carries `ingressClassName` (default `traefik`) plus a `tls:` block referencing the env's **per-env wildcard cert Secret** (`<tenant>-<env>-wildcard-tls`), and the CLI prints `https://<hostname>`, when something will actually populate that Secret — see the two cases below. Otherwise `expose` resolves to the same plain-http Ingress `--no-tls` asks for explicitly (no `tls:` block, `ingressClassName` only), and the CLI prints `http://<hostname>`: referencing a Secret nothing will ever populate would make the Ingress claim `https` while traefik served its own self-signed certificate. `ExposeServiceResult.tlsDisabledReason` (`"--no-tls"` or naming the missing DNS-01 broker config) is set whenever `tlsEnabled` is `false`, so a caller can tell the two cases apart. Issuance itself — populating the Secret at all — is resolved one of two ways depending on who owns the environment:

- **The platform's own environment** (e.g. `frs-prod`) issues its cert via a one-time `terraform-erun-cluster-edge` apply (the `erun-enable-hosting-edge` skill): cert-manager + a **namespaced** DNS-01 `Issuer` in the env's own namespace (not a cluster-scoped `ClusterIssuer` — so an env can only ever use its own issuer) issue a per-env wildcard `*.<tenant>-<env>.<services-zone>` Certificate. The issuer's DNS-01 solver is `cloudflare` on a Cloudflare-served zone, or `powerdns-rfc2136` (DNS UPDATE + TSIG straight to the platform's authoritative PowerDNS) once the services zone is delegated off Cloudflare.
- **Every other hosted environment** gets the same namespaced-Issuer shape provisioned automatically, through `expose` itself rather than a manual terraform apply (#1093). When `--dns01-token-file`, `--dns01-broker-url`, and `--acme-email` are all set, `expose` additionally applies, into the env's own namespace: a Secret carrying the per-env DNS-01 broker token (read from the file at `--dns01-token-file`, never from argv), a namespaced `Issuer` using the `dns01.webhook` solver (`solverName: powerdns-broker`, `groupName` from `--dns01-webhook-group-name`, default `acme.erun.io`) scoped via `selector.dnsZones` to just `<tenant>-<env>.<services-zone>`, and a `Certificate` for `*.<tenant>-<env>.<services-zone>` referencing it. `--acme-server` defaults to Let's Encrypt production. The webhook shim (a per-cluster singleton, installed by `terraform-erun-cluster-edge`'s `install_dns01_webhook` — on by default when that module's own `dns01_provider` is `powerdns-broker`, but settable independently so a platform's own wildcard can stay on a different solver) forwards each challenge to the DNS-01 broker carrying this token; the broker verifies it and authorizes the write only within the token's own `<tenant>-<env>` subzone (`erun-backend-api/internal/dns01broker.AuthorizeChallenge`), so one environment's namespace-admin RBAC can never obtain a certificate for another environment's hostname even though every environment's certificate is issued through the one central broker. The hosted deploy Job supplies all three flags automatically once the backend has `api.acmeEmail` configured, minting the token itself from the backend's MCP-signing key (`mcptoken.Signer.SignDNS01`) — see [Hosted platform · Per-env TLS certificate provisioning](/concepts/hosted-platform#per-env-tls).

Either way, `expose` **references** the pre-issued Secret and sets **no** `cert-manager.io/issuer` annotation on the Ingress itself (the annotation model would trigger per-host issuance instead of the one wildcard cert covering every exposed service).

**Idempotency / errors.** `replace-rrset` and `apply` are both idempotent; re-running converges. The wildcard record is written before the Ingress, so a failure applying the Ingress can leave the DNS record in place — re-run after resolving the cluster issue. Pre-flight validation (missing/malformed `platform:` block, missing `--ip`, non-DNS-1035 service name) fails before any write; see [`erun expose` · Error behaviour](/cli/expose#error-behaviour).

## Listing services

`erun services <tenant> <env>` (CLI and the `services` MCP tool) discovers what `erun expose` otherwise needs an operator to already know: every Service in the env's namespace, its ports, and whether it is already reachable at a public hostname. Read-only; no `platform:` block required.

**Inputs.** `tenant`, `env` (positional); `--dry-run`.

**Execution.** Two calls, in order, both traced before either runs:

1. `kubectl [--context <env-ctx>] -n <tenant>-<env> get service -o json`
2. `kubectl [--context <env-ctx>] -n <tenant>-<env> get ingress -o json`

**Resolution.** For each real Service:

1. Build a map from backend Service name → `{hostname, scheme}` by walking every Ingress named `expose-<label>` (the name `erun expose` gives every Ingress it applies): for each such Ingress, every Service named in `spec.rules[].http.paths[].backend.service.name` maps to that Ingress's first `spec.rules[].host` and to `https` if that host also appears in `spec.tls[].hosts`, else `http`. This is the cross-reference `erun expose` itself never had to make (it always writes its own Ingress against the Service name it just resolved) — reading the Ingress's own backend reference back out is what lets a caller trust "already exposed" without assuming the naming convention below held when the Ingress was created.
2. A Service present in that map reports `exposed: true` with the mapped `hostname`/`scheme` — ground truth, not derived.
3. A Service absent from that map is checked against the naming convention `erun expose`'s `<tenant>-<service>` backend derivation depends on: if its name has the `<tenant>-` prefix, `exposableLabel` is the remainder (the value to pass as `erun expose`'s `<service>` argument). If it doesn't — e.g. a Service a repo's own chart named without that convention — `exposableLabel` is empty: `expose` has no way to route to it correctly (see [Platform service exposure](#platform-service-exposure) above), so a caller must not offer to expose it.

**Errors.** A `kubectl` `Forbidden` response on either call is reported distinctly (wrapping a sentinel error) so a caller can render "you may not see this" rather than an empty list.

## Unexposing

`erun unexpose <tenant> <env>` (CLI and the `unexpose` MCP tool) removes the per-env wildcard DNS record `expose` created — the counterpart `expose` never had until #1094. It touches only the platform DNS zone: the Ingress that referenced the record lives in the env's own namespace and is torn down with the namespace, so there is nothing else for `unexpose` to remove.

**Inputs.** `tenant`, `env` (positional); `--skip-if-unconfigured`; `--services-zone`/`--platform-namespace` (override, exactly like `expose`'s own flags of the same name, for a sourceless caller with no project checkout); `--dry-run`.

**Resolved plan.**

| Field | Value |
|---|---|
| Per-env wildcard record | `*.<tenant>-<env>.<servicesZone>` |
| Services zone | `platform.serviceszone` (defaults to `services.<platform.basedomain>`) |

**Execution.** One side effect: `kubectl [--context <platform-ctx>] -n <platform-namespace> exec deploy/<platform-tenant>-powerdns -- pdnsutil --config-dir=/etc/pdns-shared delete-rrset <zone> <rel-name> A` — the delete-side counterpart to `expose`'s `replace-rrset`, sharing the same argv-building helper so the `--config-dir` flag (required for `pdnsutil` to find the shared PowerDNS config; its absence reads as a missing zone rather than a misconfigured tool) can never drift between the two.

**Env teardown.** `erun platform env delete`'s delete Job chains a best-effort `erun unexpose --skip-if-unconfigured` after a successful `erun delete`, symmetric with the deploy Job chaining `erun expose` itself (#1094). A cleanup failure does not fail the delete — the namespace already tore down — it is logged on the control plane, since the environment row that would otherwise carry the failure reason is removed in the same workflow step (see [`DELETE /v1/environments/{environment_id}`](/agent-reference/api-protocol#delete-endpoint) for the asynchronous delete lifecycle this runs inside).

## Cross-namespace traffic semantics

Vanilla Kubernetes lets pods reach across namespaces, so ERun provides a default-deny `NetworkPolicy` as a **copy-paste pattern you apply per env** — the runtime chart does **not** auto-deploy one (no `NetworkPolicy` template ships in it). Apply this manifest to an env's namespace to block ingress from outside it. The shape:

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: default-deny-cross-ns
  namespace: <tenant>-<env>
spec:
  podSelector: {}                    # applies to every pod in the namespace
  policyTypes: [Ingress]
  ingress:
    - from:
        - podSelector: {}            # only same-namespace pods are allowed in
```

### Opt-in cross-env ingress

To allow `<tenant>-env-a` to reach `<tenant>-env-b/<service>`, add an ingress policy on the target namespace that selects the source by namespace label:

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: allow-shared-<service>
  namespace: <tenant>-env-b
spec:
  podSelector:
    matchLabels: { app: <service> }
  policyTypes: [Ingress]
  ingress:
    - from:
        - namespaceSelector:
            matchLabels: { allow-shared-<service>: "true" }
```

Then label the consumer namespace:

```bash
kubectl label namespace <tenant>-env-a allow-shared-<service>=true
```

The runtime chart can apply this label via a `values.yaml` flag (`runtime.sharedDbConsumer: true`) so the policy is committed in the source rather than applied ad-hoc.

## Egress semantics

Outbound traffic from any env is unrestricted by default — the default-deny policy only governs `Ingress`. To restrict egress, apply a separate policy:

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: restrict-egress
  namespace: <tenant>-<env>
spec:
  podSelector: {}
  policyTypes: [Egress]
  egress:
    - to:
        - namespaceSelector:
            matchLabels: { name: kube-system }    # DNS
      ports:
        - protocol: UDP
          port: 53
    - to:
        - ipBlock:
            cidr: 0.0.0.0/0
            except:
              - 169.254.0.0/16                    # instance metadata
              - 10.0.0.0/8                        # internal RFC1918
      ports:
        - protocol: TCP
          port: 443
```

This is a per-env decision: agent envs typically need broad outbound (image pulls, `go mod download`, `npm install`); runtime envs in prod can be locked down.

## Port-forward state files

The CLI owns the local port-forwards: `erun open` starts one detached `kubectl port-forward` process per channel (MCP, SSH, API) and records each in a state file at `<UserConfigDir>/erun/portforward/{mcp,sshd,api}/<tenant>/<env>.json`. They are **best-effort**: the shell/AI session runs in-pod via `kubectl exec` and does not use them, so a forward that cannot bind is logged as a warning and skipped rather than aborting `open`. The desktop app does not write these files — it reads the convention (that is how the local MCP port reaches laptop-side Agent clients) and re-runs `erun open --no-shell` when a forward needs re-establishing; because that re-run only fails when the runtime is genuinely undeployed (not on a forward that can't bind), the "deploy this environment" prompt it drives stays accurate.

```json
{
  "tenant": "my-tenant",
  "environment": "local",
  "kubernetesContext": "orbstack",
  "namespace": "my-tenant-local",
  "localPort": 17000,
  "logPath": "/home/sam/.config/erun/portforward/mcp/my-tenant/local.log",
  "processId": 84231
}
```

| Field | Type | Meaning |
|---|---|---|
| `tenant` | string | Tenant the forward belongs to. |
| `environment` | string | Environment the forward belongs to. |
| `kubernetesContext` | string | Kubernetes context the `kubectl port-forward` runs against. |
| `namespace` | string | Target namespace, `<tenant>-<env>`. |
| `localPort` | int | The `127.0.0.1` port bound on the laptop — the port a client calls. |
| `logPath` | string, optional | The forward's log file: the state-file path with `.json` replaced by `.log`. |
| `processId` | int, optional | PID of the detached `kubectl port-forward` process. |

The `sshd` file can additionally carry `forwardPort` (int) and `proxyProcessId` (int) — legacy fields from a removed local-proxy design. They are no longer written; when either is present, `erun open` treats the forward as stale and restarts it.

On each open, `erun open` reconciles the recorded state per channel:

1. If the identity fields (`tenant`, `environment`, `kubernetesContext`, `namespace`, `localPort`) match the env being opened and the local endpoint passes the channel's liveness probe (HTTP `GET /mcp` for MCP, HTTP `GET /healthz` for API, an `SSH-` banner read for SSH), the existing forward is reused.
2. Otherwise, if the identity fields match and the recorded `processId` still holds the port, that process is stopped.
3. If the local port is still in use, the holder is inspected: a `kubectl port-forward` whose argv matches the one `erun open` would start itself is adopted — the state file is rewritten with that PID — while any other holder aborts with `local <channel> port <n> is already in use by <holder>`.
4. Otherwise a new detached `kubectl port-forward` is started, its output appended to `logPath`, and the state file is rewritten with the new PID.

When the forwarded `processId` exits, the file is left in place for diagnostic purposes; the next `erun open` overwrites it. `erun delete` does not remove these files either — a later env with the same name simply overwrites them.

`UserConfigDir` follows Go's `os.UserConfigDir`: `~/Library/Application Support` on macOS, `$XDG_CONFIG_HOME` or `~/.config` on Linux, `%AppData%` on Windows. Installs that predate this layout kept the state under `os.UserCacheDir` (`<UserCacheDir>/erun/{mcp,sshd,api}/...`); the first access after upgrading silently renames each file (and its log) into the config-dir path.

## What ERun doesn't manage

| Concern | Owned by |
|---|---|
| Ingress controller installation | Cluster admin. Install via the cluster's normal mechanism (`helm install ingress-nginx`, …). |
| DNS records | DNS provider (Route53, Cloudflare, …). |
| TLS certificate issuance | cert-manager + ACME (Let's Encrypt / ZeroSSL) or a private CA. |
| Service mesh sidecars | Application teams. The runtime pod does not require a sidecar; if one is added, it is applied per the cluster's mesh convention. |

## See also

- [Networking](/concepts/networking) — Operator-facing summary.
- [Security model](/concepts/security) — namespace + service-account isolation.
- [Inside an environment](/concepts/runtime-pods) — what lives inside the namespace.
