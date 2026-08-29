---
name: erun-onboard-service
description: Adopt a repository that already has its own layout into erun — discover where its Dockerfiles and charts actually live, wire `.erun/config.yaml` to that layout without moving a single file, preflight the environment for the failures that surface far from their cause, then build, deploy, and expose one of its services at HTTPS with a valid certificate (public hostname or localhost). Use when the user says "start using erun in this repo", "onboard this service to erun", "this repo has its own structure", "expose this service with a valid cert", "make this service reachable over https", "serve it on localhost with a valid certificate", "wire this repo up to erun", "deploy a service from a custom repo layout", or any similar request to bring an existing, non-conventional repository under erun and get one of its services served over TLS.
---

# Onboard an existing repo's service into erun

A repo that already ships its own `docker/` and `k8s/` directories does not need
scaffolding — it needs erun pointed at what is already there, an environment that
can actually build and pull, and a certificate. This skill does those three, in
that order, and verifies the result by fetching it over HTTPS.

**Not this skill:** a service with *no* Dockerfile or chart yet. That is
`erun-blueprint-service`, which writes those artifacts in erun's conventional
`<tenant>-devops/{docker,k8s}/<component>/` layout. This skill is the opposite
direction — the artifacts exist, in the repo's own shape, and must stay where
they are.

## Step 1 — Discover, do not assume

Never guess the layout. Enumerate it, then say what was found:

```sh
find . -name Dockerfile -not -path '*/node_modules/*' -not -path '*/.git/*' | head -50
find . -name Chart.yaml -not -path '*/node_modules/*' -not -path '*/.git/*' | head -50
```

Group the hits into candidate services by their common parent. A repo with
several independent products (`harnesses/<name>/{docker,k8s}/<component>`,
`services/<name>/…`, `apps/<name>/…`) yields one candidate per product, each with
its own components. Report the list and the one being onboarded; do not silently
pick.

For each candidate record: the component name (the directory under `docker/`),
the chart path, whether a `values.yaml` exists, and what the Docker build context
needs to be — a Dockerfile that copies from the repo root needs
`paths.dockercontext: repo-root`, and getting this wrong fails as a missing file
during `COPY`.

## Step 2 — Wire `.erun/config.yaml` to that layout

The project config is what teaches erun a non-conventional layout. Write it in
the environment's worktree; it is normally gitignored, so it does not commit:

```yaml
paths:
  docker: <path>/docker
  k8s: <path>/k8s
  dockercontext: repo-root
environments:
  <env>:
    k8s:
      deployments:
        - <component>
```

**`paths.docker` and `paths.k8s` are single-valued.** A repo with several service
roots is onboarded one root at a time, and the config is swapped between them.
Say this out loud when the repo has more than one candidate — a half-configured
tree that builds the wrong product is the failure this prevents.

## Step 3 — Preflight before building anything

Each check below has cost hours when skipped, because every one of them surfaces
far from its cause. Run them all, report pass/fail with the remedy, and stop on a
fail rather than discovering it mid-build.

1. **Cluster-registry access.** When the env builds to an in-cluster registry,
   its ServiceAccount must be able to resolve it:
   ```sh
   kubectl auth can-i get services -n kube-system \
     --as="system:serviceaccount:<namespace>:<tenant>-devops"
   ```
   `no` → every `erun build`/`erun push` dies at
   `kubectl get svc kube-system/erun-registry: Forbidden`. Remedy: a Role +
   RoleBinding in `kube-system` granting `get/list services`, `get/list pods`,
   and `create pods/portforward` to that SA.
2. **dind insecure registry.** An insecure in-cluster registry needs
   `--insecure-registry` on the dind sidecar, which comes from the chart
   (`clusterRegistryInsecure`) — `/etc/docker` is read-only, so it cannot be
   hand-edited in the pod.
3. **Every image the plan needs is pullable.** Check the ones the deploy
   references *and* the ones any edge module references, at the exact version:
   ```sh
   tok=$(curl -s "https://ghcr.io/token?scope=repository:<repo>:pull&service=ghcr.io" | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')
   curl -s -o /dev/null -w '%{http_code}\n' -H "Authorization: Bearer $tok" \
     "https://ghcr.io/v2/<repo>/manifests/<version>"
   ```
   A `403` here becomes an `ImagePullBackOff` three layers later, with the
   symptom (no certificate) nowhere near the cause.
4. **Cluster-scoped rights, only if the edge is missing.** Installing
   cert-manager or an ingress controller creates namespaces and CRDs. A normal
   env SA cannot; run that part from an admin kubeconfig, or make the env a
   platform account. Check what is already there before installing anything:
   ```sh
   kubectl get crd clusterissuers.cert-manager.io >/dev/null 2>&1 && echo "cert-manager present"
   kubectl get pods -A | grep -Ei 'traefik|ingress-nginx' | head
   ```
5. **The committed values are frequently a production config.** Read the chart's
   `values.yaml` for auth modes, live-LLM or connector switches, ingress, and a
   database URL. If it cannot boot without secrets the environment does not have,
   the first deploy needs a non-prod override file — plan it now, do not debug a
   CrashLoopBackOff later.

## Step 4 — Build and deploy

```sh
erun push --build                       # in the pod; builds and publishes the component
erun deploy <tenant> <env> --components <component> [-f <override>.yaml]
```

The override from Step 3.5 goes in as a values file. Two rules learned the hard
way: an empty-stub secret is worse than no secret (an empty `DATABASE_URL`
breaks a driver's connect at import time, so prefer an explicitly empty
`env.secret: []` plus a real value), and a build gate inside the Dockerfile
(a test suite in the builder stage) is the repo team's to fix, not something to
bypass silently — report it and stop.

## Step 5 — Expose with a valid certificate

**The name matters before anything else.** `erun expose <tenant> <env> <service>`
resolves the tenant-scoped Service `<tenant>-<service>`. A repo-native chart
usually renders a Service named literally (`validation-agent-backend-api`), which
no `<service>` argument can reach. Check first:

```sh
kubectl -n <namespace> get svc
```

If the rendered name does not start with `<tenant>-`, expose it with an Ingress in
the repo's own chart pointing at the real Service name, rather than renaming a
Service the repo owns.

Then choose the DNS-01 path for the certificate:

- **`cloudflare`** — the tenant's own zone and a delegated token
  (`erun cloud init cloudflare`, then attach the alias to the env, which injects
  `CLOUDFLARE_API_TOKEN`). Simplest when the tenant controls the zone.
- **`powerdns-broker`** — the multi-tenant-safe path: register the tenant on the
  platform (`erun cloud init erun --api-url <platform-api>`, `erun cloud login`,
  `erun platform tenant create`), take the per-tenant token, and let the namespaced
  Issuer broker its challenges through the webhook shim, authorized against the
  env's own subzone. Confirm the shim image is pullable (Step 3.3) before choosing
  this.
- Do **not** reach for `powerdns-rfc2136` on a shared cluster; its zone-wide TSIG
  key is an impersonation hole, as the module's own variable documentation says.

Apply the edge with `erun-enable-hosting-edge`, pinned to the erun version the env
runs. Issue against the ACME **staging** server first, then switch to production
once a challenge completes — a misconfigured loop against production burns rate
limits that take a week to clear.

**Localhost with a valid certificate.** A locally-trusted CA (mkcert and friends)
produces a certificate that is only valid on the machine that trusts it. Prefer a
real one: issue for the public hostname as above, then resolve that hostname to
`127.0.0.1` in the host's `hosts` file and reach the local ingress. The browser
validates a genuine chain and no traffic leaves the machine. On WSL2-hosted
clusters the ingress's published ports are already reachable on Windows
`localhost`.

## Step 6 — Verify the artifact, not the object

An Ingress existing proves nothing. Verify in this order:

```sh
# Certificate readiness: select by condition TYPE, never by array index.
kubectl -n <namespace> get certificate <name> \
  -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}'
kubectl -n <namespace> wait --for=condition=Ready certificate/<name> --timeout=5m
```

An `Issuing` condition sitting at index 0 reads as `Ready` to an indexed lookup,
which reports a pending challenge as an issued certificate.

Then fetch the service over HTTPS and check the chain and the SAN — not just a
200:

```sh
curl -sS -o /dev/null -w '%{http_code} %{ssl_verify_result}\n' "https://<hostname>/<health-path>"
echo | openssl s_client -connect <hostname>:443 -servername <hostname> 2>/dev/null \
  | openssl x509 -noout -issuer -subject -dates -ext subjectAltName
```

`ssl_verify_result` must be `0`, the issuer must be the real CA, and the SAN must
cover the hostname actually being used.

## Error behaviour

| Failure | What to do |
|---|---|
| Several candidate services found | Report them all and onboard one; never pick silently. |
| `COPY` fails during build | Build context is wrong — set `paths.dockercontext: repo-root`. |
| `kubectl get svc kube-system/erun-registry: Forbidden` | The SA Role from Step 3.1 is missing. Add it; do not switch registries to dodge it. |
| Image manifest returns 403/404 at the pinned version | Stop. Mirror it into the in-cluster registry or make the package pullable — an unpullable image cannot be deployed around. |
| Pod CrashLoopBackOff on first deploy | Almost always the production values from Step 3.5. Apply the non-prod override. |
| Builder-stage test gate fails | The repo team's fix. Report it; do not bypass the gate to get a green deploy. |
| `erun expose` resolves but the Ingress 503s | The Service name is repo-native, not `<tenant>-<service>`. Add an Ingress to the repo's chart instead of renaming the Service. |
| Certificate never becomes Ready | Walk `Certificate → CertificateRequest → Order → Challenge` for the real reason; the usual causes are an unpullable webhook shim, a token that cannot write the subzone, or a hostname outside the issued zone. |
| `ssl_verify_result` non-zero | The chain is not publicly trusted — a staging ACME certificate, or a local CA. Say so plainly rather than reporting "HTTPS works". |

## Context

Gate pod-only fragments on `[ -n "${ERUN_TENANT:-}" ]`; on a laptop, `kubectl`,
`helm` and `erun` may not be on PATH. Anything the operator will want off the pod
(a preflight report, a certificate dump) goes to `${ERUN_OUTPUTS_DIR}` when it is
set, not into the worktree.
