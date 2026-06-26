---
title: Deploying the platform
---

# Deploying the platform

How an operator stands up a hosted erun platform (for example `erunpaas.com`). The platform's Kubernetes charts, Terraform modules, and deploy skills all live in **erun itself** — you build erun, run that image in a runtime environment, and its skills apply the k8s + Terraform. You don't hand-run `kubectl` or `terraform`, and you don't scaffold a custom image for this.

## Two environments

- **`<tenant>-local`** — your dev workbench, **created first** (`erun init`). The platform's k8s charts (`erun-devops/k8s`), Terraform modules (`erun-devops/terraform-erun`), and deploy skills are part of the erun codebase; here you set the platform **deploy parameters** and `erun build --release` / `erun push` the erun image. The image carries the skills + tooling and references the published charts (OCI) and Terraform modules (GitHub) — it doesn't bake them. **No Cloudflare token here.** Later, once the platform is up, you set up this env's own DNS zone + TLS through the running erun API.
- **`<tenant>-prod`** — a runtime-only environment **created from the built image**. Here you **supply the Cloudflare token** and bootstrap: deploy the erun API + components, then the skills apply the k8s + Terraform to **bootstrap the Cloudflare hosts** (DNS zones + wildcard TLS), pinned to the running version.

## Order

"Skill" = an agent skill you invoke by intent; the rest are `erun` commands. Watch the **Where** column — the sequence moves local → prod → back to local.

| # | Where | Invoke | What it does | Status |
|---|---|---|---|---|
| 1 | laptop / desktop | `erun init` | Create the **`<tenant>-local`** dev workbench. | Ready |
| 2 | `<tenant>-local` | Set the platform **deploy parameters** ([`platform:`](/reference/configuration#platform-block)) | `basedomain` (e.g. `erunpaas.com`), `acmeemail`, `authoritativeip`, `env: <tenant>-prod`. erun threads these into the PowerDNS helm values, the edge Terraform vars, and `expose`. No Cloudflare token. | Ready |
| 3 | `<tenant>-local` | `erun build --release` → `erun push <version>` | Build + publish the **erun** image (carrying the deploy skills + tooling) and the k8s charts (OCI), at a version. The Terraform modules are referenced from GitHub. | Ready (incl. the API + edge once PRs #681/#688 merge) |
| 4 | laptop / desktop | `erun init` (runtime env, pinned to `<version>`) | Create **`<tenant>-prod`** from the built image. | Ready |
| 5 | `<tenant>-prod` | `erun deploy <version>` | Deploy the platform components: the **erun API**, Postgres, DB migrations, PowerDNS, and the docs publish. | Ready |
| 6 | `<tenant>-prod` | Provide the Cloudflare API token **here** | Supply the token in prod, not local, so the edge can bootstrap the DNS zones. | Ready |
| 7 | `<tenant>-prod` | skill **`erun-enable-hosting-edge`** | Bootstrap the Cloudflare hosts: Traefik + cert-manager + the Cloudflare DNS-01 wildcard-TLS issuer, using the supplied token (fetches the Terraform module from GitHub). | Ready (PR #688) |
| 8 | `<tenant>-prod` | `erun expose <tenant> <env> mcp --ip <ingress-ip>` | Publish an environment's MCP at `mcp.<tenant>-<env>.services.<base-domain>` (any service the same way). | Ready (HTTPS once the `expose` TLS wiring lands) |
| 9 | `<tenant>-local` | Use an **erun API token** against the running platform | Set up this local env's own DNS zone + TLS certs through the deployed erun API. | Planned |
| 10 | `<tenant>-prod` | skill **`erun-deploy-platform`** | One-shot orchestration of steps 5–8. Until it lands, run them individually. | Planned |

Alongside these, stand up the **OIDC issuer** (Zitadel) at `auth.<base-domain>` and point the API + console at it; the first sign-in bootstraps the `OPERATIONS` tenant. Zitadel as a managed erun component is `(Planned.)`.

## Which skills, and where

| Skill | Environment | Purpose | Status |
|---|---|---|---|
| `erun-enable-hosting-edge` | `<tenant>-prod` | Bootstrap the Cloudflare hosts: ingress + cert-manager + DNS-01 wildcard TLS for the services zone. | Ready |
| `erun-deploy-platform` | `<tenant>-prod` | End-to-end prod bootstrap (orchestrates deploy → edge → expose). | Planned |

`erun-build-env` and the `erun-blueprint-*` skills are **not** part of the platform deploy. They build a *tenant's own* custom image (its toolchain) or scaffold application code; they do not — and should not — produce the platform's k8s charts or Terraform modules, which live in erun and ship by building it.

## What the operator does *not* do

You do not run `kubectl`, `helm`, or `terraform` by hand, you do not scaffold a custom image for the platform, and you do not copy charts or modules around: the built erun image + the skills handle the Kubernetes and Terraform, pulling the charts from OCI and the Terraform modules from GitHub. The Cloudflare token is supplied only in `<tenant>-prod`, at bootstrap time. The genuinely manual, one-time external setup is the domain + Cloudflare account + the DNS delegation cutover — see [Cloud setup](/deployment/cloud-setup).
