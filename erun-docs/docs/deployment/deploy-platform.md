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
| 10 | `<tenant>-prod` | `erun deploy --version <version> --components <tenant>-console` | Deploy the **hosted web console** at `console.<base-domain>`. See [The console](#the-console) — it needs a DNS record and (in production) a cert-manager issuer name first. | Ready |
| 11 | `<tenant>-prod` | `erun expose <tenant> <env> mcp --ip <ingress-ip>` | Publish an environment's MCP at `mcp.<tenant>-<env>.services.<base-domain>`. For a runtime environment created through the hosted provisioning API instead of `erun init`, this step runs automatically — see [Hosted platform · Automatic exposure](/concepts/hosted-platform#automatic-exposure). | Ready |
| 12 | `<tenant>-local` | Use an **erun API token** against the running platform | Set up this local env's own DNS zone + TLS certs through the deployed erun API. | Planned |
| 13 | `<tenant>-prod` | skill **`erun-deploy-platform`** | One-shot orchestration of steps 6–11. | Planned |

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

**4. The console's SPA client.** The component creates and maintains this for you — do not register it by hand in Zitadel's own console, because the next reconcile tick (every few minutes) overwrites a manually-registered redirect URI with the configured one. It registers a **User Agent**, **Authorization Code + PKCE** application whose redirect and post-logout-redirect default to `platform.consoleUrl`; set `zitadel.oidc.consoleRedirectUri` to override that default, and add `zitadel.oidc.additionalConsoleRedirectUris` when the console is reachable at more than one address (e.g. moving it to a new host without a sign-in outage on the old one — both stay registered until you remove the old address from the list). The console no longer needs rebuilding with these values — it resolves the issuer and this application's client id at runtime from [`GET /v1/platform`](/agent-reference/api-protocol#platform-endpoint), so one built console image serves any instance. See [The console](#the-console) below for wiring that endpoint's values.

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

## The console {#the-console}

The hosted web console (`erun-console` chart) is a static SPA served **same-origin** with the erun API — its own nginx proxies `/v1/*` to the API's in-cluster Service, so the browser never makes a cross-origin call and the API needs no CORS headers. Deploy it like any other component:

```bash
erun deploy --version <version> --components <tenant>-console
```

Two things are yours to supply; the chart does the rest.

**1. DNS.** `console.<base-domain>` needs an `A` record pointing at the cluster's ingress IP, the same way `auth.<base-domain>` does for the hosted IdP.

**2. The certificate.** Set `console.certManagerIssuer` to the edge's DNS-01 Issuer and cert-manager fills the Ingress's TLS Secret for you. Leave it empty only if you issued the certificate yourself and named it in `console.tlsSecretName`. The host itself defaults to `console.<base-domain>` from the env's `platform:` block; set `console.externalDomain` explicitly to override it.

### The apex and www redirect {#apex-redirect}

The bare `<base-domain>` and `www.<base-domain>` are otherwise dead ends — only the console host resolves to anything. Whenever `platform.baseDomain` is set, `erun-console` also redirects those two hosts to the console with a permanent (301) redirect, so someone who types your product's own domain lands on the console instead. It stays a redirect rather than a third origin the console is also served on, because sign-in (Authorization Code + PKCE) depends on being reachable at exactly one origin.

Two things beyond the console's own DNS/certificate above:

1. **DNS for the apex and www hosts.** Apply the published `terraform-erun-cloudflare-apex` module (in your `terraform-<tenant>/` tree, the same way you reference `terraform-erun-cluster-edge`) — it writes `A` records for the apex and `www` directly into your already-Cloudflare-managed apex zone, pointing at the cluster's ingress IP. It does not create a zone (unlike `terraform-erun-cloudflare-services`, which delegates a *new* child zone) — your apex zone already exists.
2. **The certificate covers all three hosts.** No extra step: the console chart already asks `console.certManagerIssuer` for a certificate covering `console.<base-domain>`, `<base-domain>`, and `www.<base-domain>` together, as long as the redirect is enabled.

Running your apex for something else already (a marketing site)? Set `console.apexRedirectEnabled: false` and neither Ingress rule nor the extra certificate SANs render — the console host keeps working exactly as before. `console.apexHost`/`console.wwwHost` override the derived hostnames for a platform whose apex host isn't simply `<base-domain>`.

**Signing in needs no console rebuild.** Unlike before, the console does not need `VITE_OIDC_ISSUER`/`VITE_OIDC_CLIENT_ID` baked into its image — it resolves the issuer and its OIDC client id at runtime from [`GET /v1/platform`](/agent-reference/api-protocol#platform-endpoint), so the same built image serves every instance. **(Planned.)** The operator-facing config for populating that endpoint's `issuer`/`consoleClientId` (so it reflects the SPA client registered in [Hosted IdP § step 4](#hosted-idp) above) is landing alongside the backend work this depends on; until it does, an unset or absent `/v1/platform` falls back to the console's own `VITE_OIDC_ISSUER`/`VITE_OIDC_CLIENT_ID` build-time values, which remain a local-dev override.

### Error behaviour

| Failure mode | What happens | Recovery |
|---|---|---|
| No external domain resolvable | `erun deploy` aborts at the chart render with `an external domain is required`; exit code 1, nothing applied | Set `basedomain` in the env's `platform:` block, or `console.externalDomain` |
| `GET /v1/platform` is absent or empty | The console falls back to its build-time `VITE_OIDC_ISSUER`/`VITE_OIDC_CLIENT_ID`; with neither configured it shows the signed-out message with no Sign in button | Configure the backend's platform discovery, or set the console's `VITE_OIDC_*` build args as a stopgap |
| `/v1/config` returns `502` through the console | The API Service the console's nginx proxies to isn't up yet, or `console.apiServiceName`/`console.apiServicePort` don't match the deployed API | Confirm `<tenant>-api` is running and the chart's proxy target matches its Service name/port |
| The apex/www redirect is enabled but no apex host resolves | `erun deploy` aborts at the chart render with `console.apexRedirectEnabled is true but no apex host could be resolved`; exit code 1, nothing applied | Set `basedomain` in the env's `platform:` block, or `console.apexHost`/`console.wwwHost`, or `console.apexRedirectEnabled=false` |
| The apex/www hosts don't resolve at all | The Helm chart's Ingress/Middleware are only half the picture — DNS is separate. Confirm the `terraform-erun-cloudflare-apex` module has actually been applied for this env | Apply it via your `terraform-<tenant>/` tree, pointed at the cluster's ingress IP |
| Visiting the apex redirects to a `404`/wrong host | `platform.consoleUrl` names a different console than this chart's own `console.externalDomain` | Align `platform.consoleUrl` with the deployed console's host, or leave it unset so the redirect falls back to `console.externalDomain` |

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
