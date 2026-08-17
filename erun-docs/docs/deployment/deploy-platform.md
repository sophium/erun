---
title: Deploying the platform
---

# Deploying the platform

How an operator stands up a hosted erun platform (for example `erunpaas.com`). You scaffold the platform's deploy artifacts and build a custom runtime image in your agent environment, then a runtime environment runs that image while `erun deploy`, `erun terraform apply`, and skills apply the Kubernetes + Terraform — you don't hand-run `kubectl` or `terraform`.

## Two environments

- **`<tenant>-local`** — your dev workbench, **created first** (`erun init`). Here a blueprint skill scaffolds thin, version-pinned wrappers that **reference erun's published artifacts** — Helm charts in `k8s/` whose `dependencies` point at erun's **OCI** charts, and Terraform roots in `terraform-<tenant>/` whose `module` source references erun's **GitHub** modules — you set the platform **deploy parameters** + your values/vars, and `erun-build-env` + `erun build --release` produce the custom image that carries this wiring (plus the deploy skills). The reusable charts/modules stay erun's; you own only the thin wrappers. **No Cloudflare token here.** Later, once the platform is up, you set up this env's own DNS zone + TLS through the running erun API.
- **`<tenant>-prod`** — a runtime-only environment **created from the built image**. Here you **supply the Cloudflare token** and bootstrap: deploy the erun API + components, then the skills apply the k8s + Terraform to **bootstrap the Cloudflare hosts** (DNS zones + wildcard TLS), pinned to the running version.

## Order

"Skill" = an agent skill you invoke by intent; the rest are `erun` commands. Watch the **Where** column — the sequence moves local → prod → back to local.

| # | Where | Invoke | What it does | Status |
|---|---|---|---|---|
| 1 | laptop / desktop | `erun init` | Create the **`<tenant>-local`** dev workbench. | Ready |
| 2 | `<tenant>-local` | skill **`erun-blueprint-platform`** | Scaffold wrapper Helm charts in `k8s/` (deps → erun's OCI charts), Terraform roots in `terraform-<tenant>/` (source → erun's GitHub modules), and per-env `values.<env>.yaml`. | Ready |
| 3 | `<tenant>-local` | Fill `values.<env>.yaml` (+ the Terraform vars) | The **env-specific deploy values**: `basedomain` (e.g. `erunpaas.com`), `acmeemail`, `authoritativeip`, etc. No Cloudflare token here. | Ready |
| 4 | `<tenant>-local` | skill **`erun-build-env`** → `erun build --release` → `erun push --version <version>` | Set up the custom `<tenant>-devops` Dockerfile and build/publish the image — it carries the scaffolded charts/Terraform + the deploy skills (charts → OCI, Terraform modules referenced from GitHub). | Ready |
| 5 | laptop / desktop | `erun init` (runtime env, pinned to `<version>`) | Create **`<tenant>-prod`** from the built image. | Ready |
| 6 | `<tenant>-prod` | `erun deploy --version <version>` | Deploy the platform components: the **erun API**, Postgres, DB migrations, PowerDNS, and the docs publish. | Ready (API + edge land with PRs #681/#688) |
| 7 | `<tenant>-prod` | Provide the Cloudflare API token **here** | Supply the token in prod, not local, so the edge can bootstrap the DNS zones. | Ready |
| 8 | `<tenant>-prod` | `erun terraform apply <tenant> prod` (or skill **`erun-enable-hosting-edge`**) | Apply the env's scaffolded `terraform-<tenant>/prod/` root — Traefik + cert-manager + the Cloudflare DNS-01 wildcard-TLS issuer — using the supplied token. You type the environment name to confirm before it applies. The skill is the guided alternative (references erun's module directly). | Ready (PR #688) |
| 9 | `<tenant>-prod` | `erun deploy --version <version> --components <tenant>-zitadel` | Deploy the **hosted IdP** that serves OIDC at `auth.<base-domain>`. See [Hosted IdP](#hosted-idp) — it needs a masterkey Secret and a DNS record first. | Ready |
| 10 | `<tenant>-prod` | `erun expose <tenant> <env> mcp --ip <ingress-ip>` | Publish an environment's MCP at `mcp.<tenant>-<env>.services.<base-domain>`. | Ready (HTTPS once the `expose` TLS wiring lands) |
| 11 | `<tenant>-local` | Use an **erun API token** against the running platform | Set up this local env's own DNS zone + TLS certs through the deployed erun API. | Planned |
| 12 | `<tenant>-prod` | skill **`erun-deploy-platform`** | One-shot orchestration of steps 6–10. | Planned |

## Hosted IdP {#hosted-idp}

Without an OIDC issuer nobody can sign in to the platform, so the IdP is not optional: the erun API rejects every call and the console has nothing to redirect to. ERun ships it as the version-pinned `erun-zitadel` component — Zitadel core plus its separate Login V2 container in one pod behind one origin — which you deploy like any other component, after the edge is up (the cert comes from the edge's issuer) and after Postgres (it stores its data there, in its own `zitadel` database).

Four things are yours to supply; the chart does the rest.

**1. The masterkey.** Zitadel encrypts its own secrets with a 32-character key that erun deliberately never generates, defaults, or commits. Create it once, in the platform env's namespace, and keep a copy somewhere you can restore from — losing it means losing the instance:

```bash
kubectl -n <tenant>-prod create secret generic <tenant>-zitadel-masterkey \
  --from-literal=masterkey="$(openssl rand -hex 16)"
```

Name it to the component as `zitadel.masterkeySecretName` in the env's `<tenant>-zitadel` values. The deploy fails loudly, before it changes anything, if the value is unset.

**2. DNS.** `auth.<base-domain>` lives in the Cloudflare-managed apex zone, not the delegated `services.` subzone, so add its `A` record alongside the other apex hosts, pointing at the cluster's ingress IP. `erun expose` does not manage this one.

**3. The certificate.** Set `zitadel.certManagerIssuer` to the edge's DNS-01 Issuer (the one `erun-enable-hosting-edge` created in the same namespace) and cert-manager fills the Ingress's TLS Secret for you. Leave it empty only if you issued the certificate yourself and named it in `zitadel.tlsSecretName`.

**4. The console's SPA client.** After the first deploy, sign in to Zitadel's own console at `https://auth.<base-domain>/ui/console` as the bootstrap admin — the username and generated password are in the `<tenant>-zitadel-admin` Secret the component created, and the first sign-in makes you change the password. Sign in with the **domain-qualified** login name the instance assigns (`<username>@<org-domain>`, shown on the user in Zitadel's console), not the bare username from the Secret. Register a project with one application: type **User Agent**, **Authorization Code + PKCE**, no client secret, redirect and post-logout-redirect `https://console.<base-domain>/`. The console is then built with `VITE_OIDC_ISSUER=https://auth.<base-domain>` and that application's `VITE_OIDC_CLIENT_ID`.

The API side needs nothing: a platform deploy already trusts its own auth host, threading `https://<auth-host>` into the API's `ERUN_OIDC_ALLOWED_ISSUERS` from the [`platform:` block](/reference/configuration#platform-block) alongside any cloud issuers. The first sign-in bootstraps the `OPERATIONS` tenant.

### Error behaviour

| Failure mode | What happens | Recovery |
|---|---|---|
| No masterkey named | `erun deploy` aborts at the chart render with `zitadel.masterkeySecretName is required`; exit code 1, nothing applied | Create the Secret and set the value |
| No auth host resolvable | Aborts with `an external domain is required`; exit code 1, nothing applied | Set `basedomain` (or `authhost`) in the env's `platform:` block, or `zitadel.externalDomain` |
| Postgres not up yet | The pod's `wait-for-postgres` init container blocks, logging `waiting for postgres`; the rollout times out rather than corrupting state | Deploy `<tenant>-backend-postgres` first — the deploy plan sequences it ahead of the IdP |
| Login container restarts on first boot | Expected and self-healing: it needs a token core writes during initialisation, so it restarts until core is up. Its startup probe reports the wait | None; watch for the pod going Ready |
| `/ui/v2/login` returns `{"code":5,"message":"Not Found"}` | Core answered a path only the Login V2 container serves — the Ingress route or the login container is missing | Confirm both containers are running and the Ingress still carries the `/ui/v2/login` path |
| Sign-in redirects to an unreachable host | The issuer names something other than the public origin | Confirm the auth host in the `platform:` block matches the DNS record and the certificate |

## Which skills, and where

| Skill | Environment | Purpose | Status |
|---|---|---|---|
| `erun-blueprint-platform` | `<tenant>-local` | Scaffold wrapper charts (`k8s/`, deps → erun's OCI charts), Terraform roots (`terraform-<tenant>/`, source → erun's GitHub modules), and per-env `values.<env>.yaml`. | Ready |
| `erun-build-env` | `<tenant>-local` | Set up the custom `<tenant>-devops` Dockerfile + build the image (carrying the scaffolded charts/Terraform + deploy skills). | Ready |
| `erun-enable-hosting-edge` | `<tenant>-prod` | Bootstrap the Cloudflare hosts: ingress + cert-manager + DNS-01 wildcard TLS for the services zone. | Ready |
| `erun-deploy-platform` | `<tenant>-prod` | End-to-end prod bootstrap (orchestrates deploy → edge → expose). | Planned |

The other `erun-blueprint-*` skills scaffold a tenant's *application* (API, docs, RLS DB), not the platform deploy.

## What the operator does *not* do

You do not run `kubectl`, `helm`, or `terraform` by hand: the built image + the skills handle the Kubernetes and Terraform. The Cloudflare token is supplied only in `<tenant>-prod`, at bootstrap time. The genuinely manual, one-time external setup is the domain + Cloudflare account + the DNS delegation cutover — see [Cloud setup](/deployment/cloud-setup).
