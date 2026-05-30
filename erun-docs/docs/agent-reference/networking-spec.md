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
  ingressClassName: nginx
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

## Cross-namespace traffic semantics

ERun's runtime chart deploys a default-deny `NetworkPolicy` per env that blocks ingress from outside the namespace. The shape:

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

The desktop maintains per-env port-forward state at `<UserConfigDir>/erun/portforward/{mcp,ssh,api}/<tenant>/<env>.json`:

```json
{
  "localPort": 17000,
  "podPort": 17000,
  "pid": 84231,
  "lastOpened": "2026-05-25T14:30:27Z"
}
```

`UserConfigDir` follows Go's `os.UserConfigDir`: `~/Library/Application Support` on macOS, `$XDG_CONFIG_HOME` or `~/.config` on Linux, `%AppData%` on Windows.

When the forwarded `pid` exits, the file is left in place for diagnostic purposes; the next `erun open` overwrites it.

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
