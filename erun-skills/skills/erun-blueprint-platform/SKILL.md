---
name: erun-blueprint-platform
description: Blueprint the deploy artifacts for a hosted erun platform — a per-env Terraform tree (terraform-<tenant>/) whose modules wrap erun's published Terraform modules, and the per-env Helm values overlays plus thin umbrella charts that reference erun's published OCI charts — all version-pinned to the erun release the environment runs. Use when the user says "blueprint the platform", "scaffold the platform terraform", "set up the platform helm charts and terraform", "create the terraform-<tenant> structure", "blueprint erun platform deploy", "set up platform deploy artifacts", or any similar request to lay down the Terraform + Helm wiring an operator applies to stand up a hosted erun platform.
---

# Blueprint a hosted erun platform's deploy artifacts

Use this skill in the **agent env** (the dev workbench, e.g. `<tenant>-local`)
to lay down the deploy artifacts an operator later applies in the **runtime
env** (e.g. `<tenant>-prod`) to stand up a hosted erun platform. It produces
two things, both of which **reference erun's published artifacts** rather than
copy them, pinned to the erun version the environment runs:

- a per-env **Terraform** tree under `terraform-<tenant>/` whose tenant modules
  wrap erun's published Terraform modules (sourced from GitHub by `ref`), and
- the per-env **Helm** values overlay (`values.<env>.yaml`) that `erun deploy`
  consumes, plus thin umbrella `Chart.yaml` wrappers that depend on erun's
  published **OCI** charts.

The reusable charts and modules stay erun's; the tenant owns only the thin
wrappers and the env-specific values. This skill packages ERun's accumulated
best practices for platform deploy wiring — the conventions encoded here are
the contract; do not freelance them.

Throughout, `<tenant>` is the env's tenant and `<env>` is the **short**
environment name (e.g. tenant `frs`, env `prod` → `terraform-frs/prod/`,
namespace `frs-prod`). The worked paths below use `frs` / `prod`.

## What this is not

- It does **not** supply the Cloudflare token or apply anything. The token is
  supplied only in the runtime env at apply time; the apply itself is
  `erun terraform apply` (see below) and `erun deploy`.
- It does **not** build the runtime image — that is `erun-build-env`, which
  produces the `<tenant>-devops` Dockerfile that bakes these artifacts + the
  deploy skills into the custom image.

## Step 1 — resolve the erun version and registry to pin to

Everything references erun at one version, so the edge and charts match the
platform the env will run.

```sh
if [ -n "${ERUN_TENANT:-}" ]; then
    # Inside a deployed env: the running version is the one to pin.
    erun_version=$(erun version | head -n 1 | tr -d '[:space:]' | sed 's/^v//')
else
    # On a laptop: read the env's pinned runtimeversion.
    grep 'runtimeversion' ~/.config/erun/<tenant>/<environment>/config.yaml
fi
ref="v${erun_version}"          # Terraform module ref + Helm chart version
```

The container registry defaults to `ghcr.io/sophium`; if the env records a
`runtimeregistry`, or marks a DEPLOY registry in `containerregistries`, use that
instead. Call it `<registry>` below.

## Step 2 — the Terraform tree

Terraform runs **inside the per-env folder**: it picks up the symlinked
`common.tf` (providers + shared locals, canonical at the root) and adds that
env's services via the folder's own `main.tf`, with values from `<env>.tfvars`.
The tenant modules in `modules/` wrap erun's published modules.

```
terraform-frs/
├── common.tf                       # providers + shared locals — CANONICAL
├── variables.tf                    # shared variable declarations — CANONICAL
├── .gitignore                      # .terraform/, *.tfplan, *.tfstate*
├── modules/
│   └── terraform-frs-cluster-edge/ # wraps erun's terraform-erun-cluster-edge
│       ├── main.tf
│       ├── variables.tf
│       └── outputs.tf
└── prod/                           # one folder per env
    ├── common.tf      -> ../common.tf      (symlink)
    ├── variables.tf   -> ../variables.tf   (symlink)
    ├── main.tf                     # THIS env's services
    └── prod.tfvars                 # env-specific values
```

There is **no `run.tf`** and **no per-env `apply.sh`/`setup.sh`/`confirm.sh`** —
`erun terraform apply` provides that workflow (Step 4).

**`terraform-frs/common.tf`** (canonical; symlinked into each env):

```hcl
terraform {
  required_version = ">= 1.3"
  required_providers {
    kubernetes = { source = "hashicorp/kubernetes", version = "~> 2.30" }
    helm       = { source = "hashicorp/helm", version = "~> 2.17" }
  }
}

# In a runtime pod these use the in-cluster service account; on a laptop, KUBECONFIG.
provider "kubernetes" {}
provider "helm" {
  kubernetes {}
}
```

**`terraform-frs/variables.tf`** (canonical; symlinked into each env). The
Cloudflare token is **not** a tfvar — it is a secret injected at apply time as
`TF_VAR_cloudflare_api_token` from the env's `CLOUDFLARE_API_TOKEN`:

```hcl
variable "cloudflare_api_token" {
  type      = string
  sensitive = true
}
variable "acme_email" { type = string }
variable "services_zone" { type = string }
```

**`terraform-frs/modules/terraform-frs-cluster-edge/main.tf`** — the tenant
module that wraps erun's published module, pinned to `<ref>`:

```hcl
variable "cloudflare_api_token" {
  type      = string
  sensitive = true
}
variable "acme_email" { type = string }
variable "services_zone" { type = string }

module "edge" {
  source = "git::https://github.com/sophium/erun.git//erun-devops/terraform-erun/modules/terraform-erun-cluster-edge?ref=v1.0.0"

  cloudflare_api_token = var.cloudflare_api_token
  acme_email           = var.acme_email
  services_zone        = var.services_zone
}
```

Substitute the real `?ref=v<erun_version>` from Step 1. Add `variables.tf` /
`outputs.tf` that pass the module's inputs and outputs through. As erun
publishes more platform modules (e.g. `terraform-erun-cloudflare-services`),
add a sibling tenant module under `modules/` the same way.

**`terraform-frs/prod/main.tf`** — instantiates the tenant module(s) for this
env:

```hcl
module "cluster_edge" {
  source = "../modules/terraform-frs-cluster-edge"

  cloudflare_api_token = var.cloudflare_api_token
  acme_email           = var.acme_email
  services_zone        = var.services_zone
}
```

**`terraform-frs/prod/prod.tfvars`** — env-specific values (no secrets):

```hcl
services_zone = "services.erunpaas.com"
acme_email    = "ops@erunpaas.com"
```

Create the env folder's `common.tf` and `variables.tf` as **relative symlinks**
to the root, so every env runs identical providers + var declarations:

```sh
mkdir -p terraform-frs/prod
ln -s ../common.tf    terraform-frs/prod/common.tf
ln -s ../variables.tf terraform-frs/prod/variables.tf
```

## Step 3 — the Helm side

Each platform component is a thin umbrella chart under
`<tenant>-devops/k8s/<component>/` that **depends on** erun's published OCI chart
(never a copy), with its **per-env values** beside it. `erun deploy` discovers
these local chart dirs as its deploy contexts; `--components=…` filters which.

**The values-file contract (this is what fails if you skip it).** For every env
it deploys, `erun deploy <tenant> <env>` reads `values.<env>.yaml` from **each**
chart directory — it is **required, per-chart, with no fallback** to a base
`values.yaml` and no config-dir overlay. A chart dir with a `Chart.yaml` but no
`values.<env>.yaml` fails the whole deploy at spec resolution:
`values file not found for environment "<env>": …/<component>/values.<env>.yaml`.
So every umbrella chart needs a `values.<env>.yaml` for **every** env it will be
deployed to.

**That includes the agent env.** `<tenant>-local` is not only the authoring
workbench — the desktop composes build→push→deploy for it (on create and via the
Deploy button), so deploying it resolves these same charts. Scaffold
`values.local.yaml` **and** `values.<runtime-env>.yaml` (e.g. `values.prod.yaml`)
for every component, or `erun deploy <tenant> local` fails on the first missing
one. (If a component genuinely should not run in an env, exclude it from that
deploy via `--components`; it still needs a values file for the envs it does run
in.)

So each component gets three files:

```yaml
# <tenant>-devops/k8s/erun-backend-api/Chart.yaml
apiVersion: v2
name: frs-backend-api
version: 0.1.0
dependencies:
  - name: erun-backend-api
    version: "1.0.0"                  # the erun release version from Step 1
    repository: "oci://ghcr.io/sophium/charts"
```

```yaml
# <tenant>-devops/k8s/erun-backend-api/values.local.yaml
# Env-specific overrides for the agent env; the published chart carries the
# defaults. Subchart values nest under the dependency name. An empty/comment-only
# file is valid (no overrides) — the file just has to exist so `helm -f` resolves.
erun-backend-api: {}
```

```yaml
# <tenant>-devops/k8s/erun-backend-api/values.prod.yaml
erun-backend-api: {}                  # add genuinely prod-specific overrides here
```

Do this for **every** component you deploy (`erun-backend-api`,
`erun-backend-postgres`, `erun-backend-db`, `erun-powerdns`, `erun-docs`, …).
Keep each values file to genuinely env-specific overrides; the published charts
carry the defaults.

**Every** erun chart — the runtime `erun-devops` chart *and* every component
chart (`erun-powerdns`, `erun-backend-*`, `erun-docs`) — publishes under the
registry's `/charts` path, kept separate from the same-named image repo
(`<registry>/<component>`) so a chart never collides with its image at the same
ref. So every dependency `repository` is `oci://<registry>/charts`, and every
dependency `version` is pinned to the erun release from Step 1.

## Step 4 — how it gets applied (not in this skill)

The operator applies these in the runtime env, never by hand-running `terraform`
or `kubectl`:

- **Terraform:** `erun terraform apply` resolves `terraform-<tenant>/<env>/`
  from the active cloud context and runs `init → fmt → plan → confirm → apply`,
  injecting `TF_VAR_cloudflare_api_token` from `CLOUDFLARE_API_TOKEN`. The
  confirm step prompts the operator to **type the environment name** before
  applying, so changes can't land in the wrong env. Preview with
  `erun terraform apply --dry-run`.
- **Helm:** `erun deploy --version <version> --components=…` (with the `values.<env>.yaml`
  overlay), then the `erun-enable-hosting-edge` skill / `erun terraform apply`
  for the edge.

## Step 5 — validate the scaffold

```sh
# Terraform: formatting + per-env structure are valid (init needs network for the module).
terraform -chdir=terraform-frs/prod fmt -check -recursive ..
terraform -chdir=terraform-frs/prod validate || true   # validate after `init` resolves the module

# Symlinks resolve and point at the canonical root files.
readlink terraform-frs/prod/common.tf      # -> ../common.tf
readlink terraform-frs/prod/variables.tf   # -> ../variables.tf

# Helm umbrellas: dependencies resolve from OCI, and every chart has a values
# file for every env it deploys to (missing one fails erun deploy — see below).
for c in <tenant>-devops/k8s/*/; do
  [ -f "$c/Chart.yaml" ] || continue
  helm dependency build "$c"
  for env in local prod; do   # every env this platform deploys to
    [ -f "$c/values.$env.yaml" ] || echo "MISSING: $c/values.$env.yaml"
  done
done
```

Commit the tree with `git` so the team shares the wiring. These are repo files,
not deliverables — they belong in the git worktree, not `${ERUN_OUTPUTS_DIR}`.

## Error behaviour

| Failure mode | Recovery |
|---|---|
| `terraform-<tenant>/` already exists | Stop. Do not overwrite. Offer to add a new `<env>/` folder into the existing tree, or confirm a different tenant. |
| The erun version can't be resolved (no `erun version`, no `runtimeversion`) | Stop and ask which erun version to pin to. Never default to `main` for production wiring — the edge and charts must match the deployed platform. |
| `terraform-erun-cluster-edge?ref=v<ver>` doesn't resolve on `terraform init` | The version has no matching git tag yet. Confirm the erun release exists; pin to the latest released `vX.Y.Z`. |
| `helm dependency build` 404s on the OCI chart | That version's chart isn't published. `erun push`/`erun release` publishes image + chart together — pin to a version that has been pushed. |
| `erun deploy` fails: `values file not found for environment "<env>": …/<component>/values.<env>.yaml` | That umbrella chart has no per-env values file for the env being deployed. Create `<component>/values.<env>.yaml` (an empty/comment-only file is valid). Remember the agent env: `values.local.yaml` is required too, since the desktop deploys `<tenant>-local`. |
| Operator asks to put the Cloudflare token in `<env>.tfvars` | Refuse. The token is a secret injected as `TF_VAR_cloudflare_api_token` from `CLOUDFLARE_API_TOKEN` at apply time — it must not be committed. |

## Important

- **Reference, never copy.** Terraform modules are sourced from erun's GitHub by
  `?ref=v<version>`; Helm charts are OCI dependencies pinned to the version. If
  you find yourself pasting erun's module or chart contents into the tenant
  tree, stop — that defeats the contract.
- **`common.tf` is canonical at the root and symlinked into each env.** Do not
  copy it per env; all envs must run identical providers and shared locals.
  Env-specific resources go in the env folder's own `main.tf`.
- **No `run.tf`, no per-env shell scripts.** `erun terraform apply` owns the
  apply workflow.
- **Every umbrella chart needs a `values.<env>.yaml` for every env it deploys
  to — including `local`.** erun deploy requires it per-chart with no fallback,
  and the desktop deploys the `<tenant>-local` agent env. A missing one fails the
  whole deploy at spec resolution.
- **Pin one erun version across both sides.** The Terraform `?ref`, the Helm
  chart `version`, and the env's `runtimeversion` move together; bump them
  together after an `erun upgrade`.
- The Cloudflare token never lives in the tree. It is supplied in the runtime
  env and injected at apply time.
