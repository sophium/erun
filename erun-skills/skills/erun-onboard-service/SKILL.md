---
name: erun-onboard-service
description: Adopt a repository that already has its own layout into erun — discover where its Dockerfiles and charts actually live (with an ignore list for the parts that are not yours), confirm which of them to roll out, wire `.erun/config.yaml` to that layout without moving a single file, preflight the environment for the failures that surface far from their cause, then build, deploy, and expose the chosen services at HTTPS with a valid certificate (public hostname or localhost). Use when the user says "start using erun in this repo", "onboard this service to erun", "this repo has its own structure", "expose this service with a valid cert", "make this service reachable over https", "serve it on localhost with a valid certificate", "wire this repo up to erun", "deploy a service from a custom repo layout", or any similar request to bring an existing, non-conventional repository under erun and get one of its services served over TLS.
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

## Step 1 — Discover, filter, then confirm what to roll out

Never guess the layout. Enumerate it:

```sh
find . -name Dockerfile -not -path '*/node_modules/*' -not -path '*/.git/*' | head -50
find . -name Chart.yaml -not -path '*/node_modules/*' -not -path '*/.git/*' | head -50
```

**One repo can hold more than one layout shape.** Both of these are common, often
side by side in the same tree, and only the first has a component directory to
take a name from:

| Shape | Component name comes from | Example |
|---|---|---|
| `<root>/docker/<component>/Dockerfile` + `<root>/k8s/<component>/Chart.yaml` | the directory under `docker/` | `harnesses/platform-validator/docker/validation-agent-backend-api` |
| `<root>/Dockerfile` + `<root>/chart/Chart.yaml` | the product directory itself | `harnesses/migration-hub/Dockerfile` |

The second shape does not fit `paths.docker`/`paths.k8s` (Step 2), which require
directories literally named `docker` and `k8s` holding per-component
subdirectories. Say so rather than half-wiring it: that repo root needs its
artifacts arranged into the first shape before erun can build it, which is
`erun-blueprint-service`'s job.

**Apply an ignore list before showing anything.** Most repos carry directories
that are nobody's deployable: vendored examples, template scaffolds, other
teams' products. Ignore by path fragment, default to skipping the obvious ones
(`_example`, `example/`, `template/`, `fixtures/`, `testdata/`), and add whatever
the operator names ("ignore everything that isn't the validator"). Say which
rules were applied and how many candidates they removed — a silent filter is how
the one service that mattered goes missing.

**Then confirm what to roll out.** Present the surviving candidates as a table —
product, shape, components, chart path, whether a `values.yaml` exists — and ask
which of them to roll out. Do not infer it from the count: one surviving
candidate is not consent to deploy it, and a product with several components
rarely wants all of them (a one-shot publisher Job next to a long-running API is
the usual pair, and only the API is being asked for). Record the answer as the
roll-out set and work only on it.

For each item in that set, record the build context: a Dockerfile that copies
from the repo root needs `paths.dockercontext: repo-root`, and getting this
wrong fails as a missing file during `COPY`.

## Step 2 — Wire `.erun/config.yaml` to that layout

The project config is what teaches erun a non-conventional layout:

```yaml
paths:
  docker: <path>/docker
  k8s: <path>/k8s
  dockercontext: repo-root
  version: <path>/VERSION      # when the product versions itself, not the repo

# Required. The BUILD registry is resolved from this file, not from the
# environment's own config: resolveDockerBuildRegistryForEnvironment reads the
# project registry list. Omit it and the build silently names its images for a
# fallback registry the environment cannot push to -- with no error, because
# nothing failed yet.
containerregistries:
  - cluster:                    # or: `registry: ghcr.io/<org>`
      service: erun-registry
      namespace: kube-system
      port: 5000
      insecure: true
    roles: [build, deploy]

environments:
  <env>:
    k8s:
      deployments:
        - <component>
```

`paths.docker` and `paths.k8s` must end in a segment literally named `docker` /
`k8s` — erun's build and deploy machinery keys off the folder name, and the
override relocates those folders rather than renaming them.

**`paths` is project-global and single-valued.** A repo with several service
roots is onboarded one root at a time, and the config is swapped between them.
Say this out loud when the repo has more than one candidate — a half-configured
tree that builds the wrong product is the failure this prevents.

**Check whether the repo commits this file.** erun's own contract is that
`.erun/config.yaml` is committed and applies to everyone who checks the repo out.
Plenty of repos gitignore `.erun/` instead (`grep -n 'erun' .gitignore`). When it
is ignored, the wiring is per-checkout: it must be rewritten in every environment
that builds this repo, and it will not survive a fresh clone. State which case
applies rather than leaving the next person to find out.

## Step 3 — Preflight before building anything

Each check below has cost hours when skipped, because every one of them surfaces
far from its cause. Run them all, report pass/fail with the remedy, and stop on a
fail rather than discovering it mid-build.

1. **Cluster-registry access.** When the env builds to an in-cluster registry,
   its ServiceAccount must be able to resolve it:
   ```sh
   sa="system:serviceaccount:<namespace>:<tenant>-devops"
   kubectl auth can-i get services -n kube-system --as="$sa"
   kubectl auth can-i get pods     -n kube-system --as="$sa"
   kubectl auth can-i create pods --subresource=portforward -n kube-system --as="$sa"
   ```
   Use `--subresource=portforward`. The `create pods/portforward` spelling reports
   a false `no` even when the permission is granted — `can-i` reads the slash form
   as a resource name, not a subresource, so it answers about a resource nothing
   grants. A preflight that trusts it condemns a working environment.

   A real `no` → every `erun build`/`erun push` dies at
   `kubectl get svc kube-system/erun-registry: Forbidden`. Remedy: a Role in
   `kube-system` granting `get/list services`, `get/list pods` and
   `create/get pods/portforward`, bound to that SA. **Look for an existing one
   first** (`kubectl -n kube-system get role,rolebinding | grep -i registry`) — a
   cluster that already runs one erun env usually has it, and the fix is adding a
   subject to the existing RoleBinding rather than minting new RBAC.
2. **dind insecure registry — downstream of check 1, not independent of it.**
   ```sh
   kubectl -n <namespace> get deploy <tenant>-devops \
     -o jsonpath='{range .spec.template.spec.containers[?(@.name=="erun-dind")]}{.args}{end}'
   ```
   No `--insecure-registry` for an insecure in-cluster registry means pushes
   fail. It is not hand-fixable (`/etc/docker` is read-only): the chart sets it
   from `clusterRegistryInsecure`, which `erun deploy` only derives once it can
   *resolve* the registry Service — which is exactly what check 1 grants. So the
   order is: fix check 1, redeploy the runtime (`erun deploy <tenant> <env>
   --current`), then re-read this arg. Fixing them in the other order does
   nothing.

   If the arg is *still* absent after that, erun did not derive the value on this
   path (it passes the registry list through unconcretized, and the chart's
   `clusterRegistryInsecure` stays empty). Patch the sidecar to exactly what the
   chart would have rendered, and say that you did:
   ```sh
   kubectl -n <namespace> patch deploy <tenant>-devops --type=strategic -p \
     '{"spec":{"template":{"spec":{"containers":[{"name":"erun-dind","args":["--insecure-registry","<clusterIP>:5000"]}]}}}}'
   ```
   The dind image store is a PVC, so this restart does not discard built images —
   but it does restart the environment's pod, which is a heads-up, and it is a
   deviation to report rather than absorb.
3. **Every image the plan needs is pullable**, at the exact version — the ones
   the deploy references *and* the ones any edge module references:
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
erun deploy <tenant> <env> --components <component>
```

**`erun deploy` has no `-f`/`--values` flag.** The Step 3.5 override is a
`values.<env>.yaml` written *beside the chart*, which erun picks up by name — and
a missing one is a hard error (`values file not found for environment "<env>"`),
not a fallback to defaults. That means onboarding necessarily adds a file to the
repo being onboarded; say so, because it is the one repo change this skill cannot
avoid.

**Run the deploy where the repo is.** For a `remote-agent` environment the
worktree lives in the pod, so the host cannot read the project config or the
chart — a host-side deploy reports `configured repo path … is not present on this
machine; no k8s.deployments plan could be read` and proceeds on defaults. Deploy
the component from inside the pod. The environment's own *runtime* chart is the
opposite case and stays a host-side operation.

Expect the build to produce **both** `linux/amd64` and `linux/arm64` — that pair
is hardcoded, with no flag or config to narrow it, so a single-architecture
cluster pays for an emulated image it can never schedule. On a local cluster that
emulated half is usually the whole wait.

**On an insecure in-cluster registry the erun publish path does not currently
complete**, and both failures look like something else:

- `erun push` pushes both per-arch images, then dies at
  `no such manifest: <registry>/<component>:<version>-amd64`. `docker manifest`
  is the one Docker subcommand that ignores the daemon's insecure-registry list;
  it needs `--insecure` of its own. Because the push aborts there, **the chart is
  never published either**.
- `erun deploy --components` then refuses, because it verifies the tenant's
  component chart in the *runtime-image* registry rather than the deploy
  registry, and probes it over forced HTTPS.

Do not read either refusal as "the chart is missing". Assemble the manifest by
hand (`docker manifest create --insecure --amend …` then
`docker manifest push --insecure …`), and if the deploy still refuses, install
the packaged chart with `helm upgrade --install` using the same
`values.<env>.yaml`. Report both as deviations rather than absorbing them.

**Publish a chart under a `charts/` prefix.** `helm push chart.tgz
oci://<registry>` writes to `<registry>/<chart-name>:<version>` — byte-for-byte
the tag the *image* occupies, so it silently overwrites the image manifest and
the pod then tries to run a chart. erun's own convention is
`oci://<registry>/charts/<name>`; use it, and if you have already clobbered the
image tag, re-create the image manifest before deploying. Two rules learned the hard
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
real one: issue for the public hostname as above and point that hostname at
`127.0.0.1`. Where a public certificate is genuinely unobtainable — no zone under
the operator's control, or a blocked DNS-01 dependency — a local CA is a
legitimate fallback, but label it as machine-local trust and never call it
"valid" without that qualifier.

Mechanics that save an hour each:

- **No `hosts` edit is needed.** `*.localtest.me` already resolves to `127.0.0.1`
  publicly, so `<name>.localtest.me` gives a real hostname for SNI and the
  certificate's SAN with no administrative change.
- **A WSL2-hosted cluster does *not* publish the ingress on Windows
  `localhost`.** The LoadBalancer binds inside the VM's own network namespace, so
  `127.0.0.1:443` on the host is refused. Forward it:
  `kubectl -n <ingress-ns> port-forward svc/<ingress> 443:443 --address 127.0.0.1`.
- **The repo's committed Ingress annotations are usually for a different
  controller.** Check `ingress.className` against what the cluster actually runs
  (`traefik` on k3s, not `nginx`) and blank the controller-specific annotations,
  or the Ingress is created and silently served by nobody.
- **Installing a local CA into the trust store is an interactive gate.** Windows
  refuses it headlessly — `Import-Certificate` reports "UI is not allowed in this
  operation" and `certutil -addstore` blocks on a dialog. Hand the operator that
  one command rather than pretending it can be automated.
- **Verify with a tool that honours `--cacert`.** Windows `curl.exe` (and Git's)
  are Schannel builds that ignore it and read the system store, so a chain that
  is provably fine fails with exit 60. Use `openssl s_client -CAfile …
  -verify_return_error`, which answers the question directly.

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
| Several candidate services found | Apply the ignore list, show what survived, and confirm the roll-out set. Never infer consent from a count of one. |
| A candidate is `<root>/Dockerfile` + `<root>/chart/` | It cannot be wired by `paths`, which need `docker`/`k8s` directories of per-component subdirectories. Say so; arranging it is `erun-blueprint-service`'s job. |
| `COPY` fails during build | Build context is wrong — set `paths.dockercontext: repo-root`. |
| Build names images for an unexpected registry | The project config has no `containerregistries`; the build reads that list, not the env's. Add it. |
| `values file not found for environment "<env>"` | There is no `-f` to pass. Write `values.<env>.yaml` beside the chart. |
| `configured repo path … is not present on this machine` | A host-side deploy of a pod-side repo. Run the component deploy in the pod. |
| `can-i create pods/portforward` says `no` | Re-check with `--subresource=portforward` before believing it; the slash form reports a false negative. |
| `kubectl get svc kube-system/erun-registry: Forbidden` | The SA is not bound to the registry-access Role. Add it as a subject to the existing RoleBinding if one exists; do not switch registries to dodge it. |
| dind still lacks `--insecure-registry` after fixing RBAC | The runtime has not been redeployed since. `erun deploy <tenant> <env> --current`, then re-read the arg. |
| Image manifest returns 403/404 at the pinned version | Stop. Mirror it into the in-cluster registry or make the package pullable — an unpullable image cannot be deployed around. |
| `no such manifest: …-amd64` on push to an insecure registry | `docker manifest` needs its own `--insecure`. Assemble by hand; the chart was not published either. |
| Deploy refuses: chart "could not be determined" in the runtime registry, or `http: server gave HTTP response to HTTPS client` | The verification probe is looking in the wrong registry, over HTTPS. Install the packaged chart with helm and report it. |
| The pod tries to run a Helm chart as its image | A `helm push` without a `charts/` prefix overwrote the image tag. Re-create the image manifest and republish the chart under `charts/`. |
| Ingress exists, nothing answers | `ingress.className` names a controller the cluster does not run. |
| `curl` exits 60 despite a good chain | Schannel curl ignores `--cacert`. Verify with `openssl s_client -CAfile`. |
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
