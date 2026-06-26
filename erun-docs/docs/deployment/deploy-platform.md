---
title: Deploying the platform
---

# Deploying the platform

How an operator stands up a hosted erun platform (for example `erunpaas.com`). The deployment is **skill-driven from a built erun image**: you prepare and build in your agent environment, and a runtime environment runs that image while skills apply the Kubernetes and Terraform — you don't hand-run `kubectl` or `terraform`.

## Two environments

- **`erun-local`** — your agent / dev environment. Where you register cloud credentials and then build and release the erun image. The image carries the skills and references the published Helm charts (OCI) and Terraform modules (GitHub) — it does not bake them.
- **`erun-prod`** — a runtime-only environment that runs the built image. Its skills deploy the platform: they pull the published chart, fetch the Terraform module pinned to the running version, apply them, and wire DNS/TLS. The Cloudflare credential the edge needs is injected into this environment from its attached Cloudflare alias.

## Order

Run these in sequence. "Skill" means an agent skill you invoke by intent (e.g. "enable the hosting edge"); the rest are `erun` commands.

| # | Environment | Invoke | What it does | Status |
|---|---|---|---|---|
| 1 | erun-local | `erun cloud init cloudflare`, then `erun cloud set <tenant>/erun-prod --alias <name>@cloudflare` | Register the Cloudflare API token as an alias and attach it to the platform environment, so its runtime pod gets `CLOUDFLARE_API_TOKEN`. | Ready |
| 2 | erun-local | Set the [`platform:` block](/reference/configuration#platform-block) in `.erun/config.yaml` | `basedomain` (e.g. `erunpaas.com`), `acmeemail`, `authoritativeip`, `env: erun-prod`. Derives the services zone, auth host, and nameservers. | Ready |
| 3 | erun-local | `erun build --release` → `erun push <version>` | Build and publish the erun image, the runtime chart (OCI), and the backend images at a version. | Ready |
| 4 | erun-prod | `erun deploy <version>` | Deploy the platform components per the env's `k8s.deployments` plan: the runtime pod, Postgres, DB migrations, the API, PowerDNS, and the docs publish. | Ready |
| 5 | erun-prod | **skill `erun-enable-hosting-edge`** | Install Traefik + cert-manager and the Cloudflare DNS-01 wildcard-TLS issuer (references the Terraform module from GitHub, applies it with the injected token). | Ready |
| 6 | erun-prod | `erun expose <tenant> <env> mcp --ip <ingress-ip>` | Publish an environment's MCP at `mcp.<tenant>-<env>.services.<base-domain>` (and any other service the same way). | Ready (HTTPS once the `expose` TLS wiring lands) |
| 7 | erun-prod | **skill `erun-deploy-platform`** | One-shot orchestration of steps 4–6: deploy the components, enable the edge, delegate DNS, and expose the services. `(Planned.)` — until it lands, run steps 4–6 individually. | Planned |

Alongside these you also stand up the **OIDC issuer** (Zitadel) at `auth.<base-domain>` and point the API and console at it; the first sign-in bootstraps the `OPERATIONS` tenant. Running Zitadel as a managed erun component is `(Planned.)` — today you deploy it yourself.

## Which skills, and where

| Skill | Environment | Purpose | Status |
|---|---|---|---|
| `erun-enable-hosting-edge` | erun-prod | Ingress + cert-manager + Cloudflare DNS-01 wildcard TLS for the services zone. | Ready |
| `erun-deploy-platform` | erun-prod | End-to-end platform bring-up (orchestrates the steps above). | Planned |

The blueprint skills (`erun-build-env`, `erun-blueprint-*`) are for building a **custom** runtime image with extra tooling or scaffolding application code — not for the platform deploy. Use them only if `erun-prod` needs tools beyond the stock image.

## What the operator does *not* do

You do not run `kubectl`, `helm`, or `terraform` by hand, and you do not copy charts or modules around: the published image + the skills handle the Kubernetes and Terraform. The genuinely manual, one-time external setup is the domain + Cloudflare account + the DNS delegation cutover — see [Cloud setup](/deployment/cloud-setup).
