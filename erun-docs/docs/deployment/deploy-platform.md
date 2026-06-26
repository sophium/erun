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
| 9 | `<tenant>-prod` | `erun expose <tenant> <env> mcp --ip <ingress-ip>` | Publish an environment's MCP at `mcp.<tenant>-<env>.services.<base-domain>`. | Ready (HTTPS once the `expose` TLS wiring lands) |
| 10 | `<tenant>-local` | Use an **erun API token** against the running platform | Set up this local env's own DNS zone + TLS certs through the deployed erun API. | Planned |
| 11 | `<tenant>-prod` | skill **`erun-deploy-platform`** | One-shot orchestration of steps 6–9. | Planned |

Alongside these, stand up the **OIDC issuer** (Zitadel) at `auth.<base-domain>` and point the API + console at it; the first sign-in bootstraps the `OPERATIONS` tenant. Zitadel as a managed erun component is `(Planned.)`.

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
