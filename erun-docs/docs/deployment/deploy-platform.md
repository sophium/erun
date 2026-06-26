---
title: Deploying the platform
---

# Deploying the platform

How an operator stands up a hosted erun platform (for example `erunpaas.com`). The deployment is **skill-driven from a built erun image**: you set up and build in your agent environment, a runtime environment runs that image, and skills apply the Kubernetes and Terraform — you don't hand-run `kubectl` or `terraform`.

## Two environments

- **`<tenant>-local`** — your agent / dev workbench, **created first** (`erun init`). Here you set up the custom runtime image's Dockerfile (the `<tenant>-devops` module) to carry the platform's **k8s + Terraform deploy setup** — the deploy skills, the platform config, and references to the published Helm charts (OCI) and Terraform modules (GitHub), fetched at apply time rather than baked — then `build` / `push` the image. **No Cloudflare token is supplied here.** Later, once the platform is up, you set up this env's own DNS zone + TLS certs through the running erun API.
- **`<tenant>-prod`** — a runtime-only environment **created from the built image**. Here you **supply the Cloudflare token** and run the bootstrap: deploy the erun API + components, then the skills use the k8s + Terraform setup to **bootstrap the Cloudflare hosts** (DNS zones + wildcard TLS), pinned to the running version.

## Order

"Skill" = an agent skill you invoke by intent; the rest are `erun` commands. Watch the **Where** column — the sequence moves local → prod → back to local.

| # | Where | Invoke | What it does | Status |
|---|---|---|---|---|
| 1 | laptop / desktop | `erun init` | Create the **`<tenant>-local`** agent/dev workbench. | Ready |
| 2 | `<tenant>-local` | skill **`erun-build-env`** | Set up the custom `<tenant>-devops` Dockerfile carrying the platform's k8s + Terraform deploy setup (skills, config, chart/module references — not baked artifacts). | Ready |
| 3 | `<tenant>-local` | Set the [`platform:` block](/reference/configuration#platform-block) | `basedomain` (e.g. `erunpaas.com`), `acmeemail`, `authoritativeip`, `env: <tenant>-prod`. No Cloudflare token here. | Ready |
| 4 | `<tenant>-local` | `erun build --release` → `erun push <version>` | Build and publish the custom image, the runtime chart (OCI), and the backend images at a version. | Ready |
| 5 | laptop / desktop | `erun init` (runtime env, pinned to `<version>`) | Create **`<tenant>-prod`** from the built image. | Ready |
| 6 | `<tenant>-prod` | `erun deploy <version>` | Deploy the platform components: the **erun API**, Postgres, DB migrations, PowerDNS, and the docs publish. | Ready (API/console surface lands with PR #681) |
| 7 | `<tenant>-prod` | Provide the Cloudflare API token **here** | Supply the token in prod, not local, so the edge can bootstrap the DNS zones (`CLOUDFLARE_API_TOKEN` becomes available to the edge skill). | Ready |
| 8 | `<tenant>-prod` | skill **`erun-enable-hosting-edge`** | Bootstrap the Cloudflare hosts: Traefik + cert-manager + the Cloudflare DNS-01 wildcard-TLS issuer, using the supplied token (fetches the Terraform module from GitHub). | Ready (PR #688) |
| 9 | `<tenant>-prod` | `erun expose <tenant> <env> mcp --ip <ingress-ip>` | Publish an environment's MCP at `mcp.<tenant>-<env>.services.<base-domain>` (any service the same way). | Ready (HTTPS once the `expose` TLS wiring lands) |
| 10 | `<tenant>-local` | Use an **erun API token** against the running platform | Set up this local env's own DNS zone + TLS certs through the now-deployed erun API. | Planned |
| 11 | `<tenant>-prod` | skill **`erun-deploy-platform`** | One-shot orchestration of steps 6–9. Until it lands, run them individually. | Planned |

Alongside these, stand up the **OIDC issuer** (Zitadel) at `auth.<base-domain>` and point the API + console at it; the first sign-in bootstraps the `OPERATIONS` tenant. Zitadel as a managed erun component is `(Planned.)` — today you deploy it yourself.

## Which skills, and where

| Skill | Environment | Purpose | Status |
|---|---|---|---|
| `erun-build-env` | `<tenant>-local` | Set up the custom `<tenant>-devops` image carrying the platform's k8s + Terraform deploy setup. | Ready |
| `erun-enable-hosting-edge` | `<tenant>-prod` | Bootstrap the Cloudflare hosts: ingress + cert-manager + DNS-01 wildcard TLS for the services zone. | Ready |
| `erun-deploy-platform` | `<tenant>-prod` | End-to-end prod bootstrap (orchestrates deploy → edge → expose). | Planned |

## What the operator does *not* do

You do not run `kubectl`, `helm`, or `terraform` by hand, and you do not copy charts or modules around: the published image + the skills handle the Kubernetes and Terraform. The Cloudflare token is supplied only in `<tenant>-prod`, at bootstrap time. The genuinely manual, one-time external setup is the domain + Cloudflare account + the DNS delegation cutover — see [Cloud setup](/deployment/cloud-setup).
