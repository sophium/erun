---
name: erun-blueprint-platform
description: Blueprint the deploy artifacts for a hosted erun platform — a per-env Terraform tree (terraform-<tenant>/) whose modules wrap erun's published Terraform modules, and the per-env Helm values overlays plus thin umbrella charts that reference erun's published OCI charts — all version-pinned to the erun release the environment runs; also maintains, repairs, and upgrades an existing terraform-<tenant>/ tree and its <tenant>-<component> umbrellas in place, re-pinning every erun reference to the target version and filling gaps against this contract. Use when the user says "blueprint the platform", "scaffold the platform terraform", "set up the platform helm charts and terraform", "create the terraform-<tenant> structure", "blueprint erun platform deploy", "set up platform deploy artifacts", "upgrade the platform terraform", "repair the platform charts", "reconcile the terraform-<tenant> tree", "bump the platform to <version>", "maintain the platform deploy artifacts", or any similar request to lay down or update the Terraform + Helm wiring an operator applies to stand up a hosted erun platform.
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

This skill is for the tenant that **deploys the erun platform itself**
(erunpaas) — its umbrellas wrap erun's *own* component charts (`erun-backend-*`,
`erun-powerdns`, `erun-docs`). A regular tenant that only runs agent envs never
uses it, and it never wraps the runtime `erun-devops` chart (see What this is
not).

**Deploy is sourceless; the Helm umbrellas are optional.** A runtime env deploys
the platform **by reference**: `erun deploy <tenant> <env> --components erun-backend-…`
installs each published `oci://<registry>/charts/erun-<component>` chart directly,
threading `tenant`/`environment` + the env's config as `--set`, with **no local
checkout** (a runtime env has no worktree). Produce the `<tenant>-<component>`
umbrella charts below **only** for the optional cases — real-time patching, or a
genuine per-env chart-value override the env config can't express. The Terraform
tree (the edge) is the part every hosted platform still needs; the umbrellas are not.

The reusable charts and modules stay erun's; the tenant owns only the thin
wrappers and the env-specific values. This skill packages ERun's accumulated
best practices for platform deploy wiring — the conventions encoded here are
the contract; do not freelance them.

Throughout, `<tenant>` is the env's tenant and `<env>` is the **short**
environment name (e.g. tenant `acme`, env `prod` → `terraform-acme/prod/`,
namespace `acme-prod`). The worked paths below use `acme` / `prod`.

## What this is not

- It does **not** supply the Cloudflare token or apply anything. The token is
  supplied only in the runtime env at apply time; the apply itself is
  `erun terraform apply` (see below) and `erun deploy`.
- It does **not** build the runtime image — that is `erun-build-env`, which
  produces the `<tenant>-devops` Dockerfile for a custom runtime image (swapped in
  via `imageOverrides.erun-devops`). It does **not** bake these platform artifacts
  into any image, and needn't: `erun deploy` installs the platform components **by
  reference** from the published registry, so a runtime env needs no local source.
- It does **not** own the runtime `<tenant>-devops` chart — `erun-build-env` does.
  The runtime pod is a universal per-env concern (every tenant has one, platform or
  not). A tenant that ships the platform components this skill produces runs them on
  its own version line, so its runtime must too: `erun-build-env` publishes a
  `<tenant>-devops` chart at the tenant version (its Step 6, **required** for such a
  tenant), and `erun deploy` rejects deploying tenant components against the shared
  `erun-devops` runtime. A bootstrap/erun-only env that deploys no components of its
  own may still ride the shared `erun-devops` chart (image swapped via
  `imageOverrides.erun-devops`). Either way, never add a runtime umbrella under
  `<tenant>-devops/k8s/` **from this skill** — the runtime chart is
  `erun-build-env`'s to own; adding one here would shadow it.

## Step 1 — resolve the erun version and registry to pin to

Everything references erun at one version, so the edge and charts match the
platform the env will run.

```sh
if [ -n "${ERUN_TENANT:-}" ]; then
    # Inside a deployed env: the running version is the one to pin. The first
    # line is "erun <version> (<commit> built <date>)" — take the 2nd field;
    # --no-registry skips the remote version lookup.
    erun_version=$(erun version --no-registry | head -n 1 | awk '{print $2}')
else
    # On a laptop: pull the value out of the env's "runtimeversion: <ver>" line.
    erun_version=$(grep 'runtimeversion' ~/.config/erun/<tenant>/<environment>/config.yaml | awk '{print $2}')
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
terraform-acme/
├── common.tf                       # providers + shared locals — CANONICAL
├── variables.tf                    # shared variable declarations — CANONICAL
├── .gitignore                      # .terraform/, *.tfplan, *.tfstate*
├── modules/
│   └── terraform-acme-cluster-edge/ # wraps erun's terraform-erun-cluster-edge
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

**`terraform-acme/common.tf`** (canonical; symlinked into each env):

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

**`terraform-acme/variables.tf`** (canonical; symlinked into each env). The
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

**`terraform-acme/modules/terraform-acme-cluster-edge/main.tf`** — the tenant
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

**`terraform-acme/prod/main.tf`** — instantiates the tenant module(s) for this
env:

```hcl
module "cluster_edge" {
  source = "../modules/terraform-acme-cluster-edge"

  cloudflare_api_token = var.cloudflare_api_token
  acme_email           = var.acme_email
  services_zone        = var.services_zone
}
```

**`terraform-acme/prod/prod.tfvars`** — env-specific values (no secrets):

```hcl
services_zone = "services.erunpaas.com"
acme_email    = "ops@erunpaas.com"
```

Create the env folder's `common.tf` and `variables.tf` as **relative symlinks**
to the root, so every env runs identical providers + var declarations:

```sh
mkdir -p terraform-acme/prod
ln -s ../common.tf    terraform-acme/prod/common.tf
ln -s ../variables.tf terraform-acme/prod/variables.tf
```

## Step 3 — the Helm side (optional — the patch/override path)

`erun deploy` installs the published component charts **by reference** from the
env's config selection, so you need these umbrella charts only for real-time
patching or a per-env chart-value override the config can't express. When you do,
each platform component is a thin umbrella chart under
`<tenant>-devops/k8s/<tenant>-<component>/` — the directory name, the `Chart.yaml`
`name:`, and the Helm release are all `<tenant>-<component>` (e.g. `acme-docs`,
`acme-backend-api`) — that **depends on** erun's published OCI chart
`erun-<component>` (never a copy), with its **per-env values** beside it. Name the
directory for the tenant, not for the erun chart it wraps: `erun deploy` keys the
component name off the directory, so `acme-docs/` deploys as `acme-docs` (wrapping
`erun-docs`), not as `erun-docs`.

Component selection is **opt-in**: `erun deploy` rolls out exactly the charts you
name via `--components` (or a saved default selection persisted in the env's
`deploy.components`), and with no selection deploys the environment's runtime
chart alone (bootstrapping/healing it). It never deploys a component just because
its chart directory is present — so a chart that should not run yet simply stays
unselected.

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
# <tenant>-devops/k8s/acme-backend-api/Chart.yaml   (dir name == chart name == <tenant>-<component>)
apiVersion: v2
name: acme-backend-api
version: 0.1.0
dependencies:
  - name: erun-backend-api
    version: "1.0.0"                  # the erun release version from Step 1
    repository: "oci://ghcr.io/sophium/charts"
```

```yaml
# <tenant>-devops/k8s/acme-backend-api/values.local.yaml
# Subchart values nest under the dependency name. Forward tenant + environment
# (see note below); the published chart carries every other default.
erun-backend-api:
  tenant: <tenant>          # e.g. acme
  environment: <env>        # the short env name, e.g. local
```

```yaml
# <tenant>-devops/k8s/acme-backend-api/values.prod.yaml
erun-backend-api:
  tenant: <tenant>
  environment: <env>        # e.g. prod
  # add genuinely prod-specific overrides here
```

Do this for **every** platform component you deploy — one `<tenant>-<component>`
umbrella dir per wrapped erun chart. This is the closed set of the erun
platform's components (`erun-backend-api`, `erun-backend-postgres`,
`erun-backend-db`, `erun-powerdns`, `erun-docs`) — **never** the runtime
`erun-devops` chart (see What this is not). E.g. `acme-backend-api/` wraps
`erun-backend-api`. Keep each values file to genuinely env-specific
overrides; the published charts carry the defaults.

**Track `Chart.lock`, ignore the built `charts/`.** Resolving an umbrella's
dependency produces two things: `Chart.lock` (the pinned, reproducible
resolution — **commit it**) and `charts/<subchart>-<version>.tgz` (the
downloaded subchart — a build artifact). `erun deploy` runs `helm dependency
build` for any umbrella chart before it installs, so `charts/` is rebuilt from
`Chart.lock` on every deploy and never needs to be committed. Add the artifact
to the repo's `.gitignore` so it doesn't land as an untracked file:

```gitignore
# Helm dependency build artifacts — rebuilt from Chart.lock by `erun deploy`.
**/charts/*.tgz
```

Commit the tgz (vendor it) only for an air-gapped install where the OCI registry
is unreachable at deploy time.

**Forward `tenant`/`environment` into each subchart.** helm does not pass
top-level values into a wrapped subchart, so a subchart that `{{ required }}`s
them (`erun-backend-api`, `erun-docs`) reads them in its **own** scope. Author
them nested under the subchart key in each `values.<env>.yaml`, as above — and
`erun deploy` satisfies the subchart on either path: a **worktree** deploy `-f`s
that file directly, and a **by-reference** deploy (a runtime env with no
worktree) both re-scopes deploy's threaded `--set`s under the subchart key *and*
`helm pull`s the published chart to `-f` this same bundled file. So a wrapped
subchart no longer fails with `tenant is required` by reference — but keep the
nested values: the worktree path depends on them, and the by-reference path
applies them. Components that don't require them (`erun-backend-postgres`,
`erun-backend-db`, `erun-powerdns`) can use `{}` — but forward them anyway if the
component reads them for labels/config. A comment-only file is still valid for
the latter; the file just has to exist so `helm -f` resolves.

**Deploy only env-appropriate components.** Selection is opt-in and per env, so a
component that can't run in an env is simply left unselected — never forced. Use
the umbrella (directory) names, `<tenant>-<component>`, in `--components` or the
saved `deploy.components`. Two real cases: `<tenant>-powerdns` binds `:53` via
`hostNetwork` and uses a private base image (`erun-powerdns:<pin>`), so it belongs
in the runtime env with a ghcr pull secret, not a local agent env (orbstack has
neither); `<tenant>-docs` is a no-op locally. For a local platform, select the
backend trio (`--components <tenant>-backend-postgres,<tenant>-backend-db,<tenant>-backend-api`);
add `<tenant>-powerdns` only where `:53` + the pull secret exist. The runtime
chart deploys on its own with an empty selection, so a bare `erun deploy <tenant>
<env>` bootstraps or heals the runtime without touching the components.

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
terraform -chdir=terraform-acme/prod fmt -check -recursive ..
terraform -chdir=terraform-acme/prod validate || true   # validate after `init` resolves the module

# Symlinks resolve and point at the canonical root files.
readlink terraform-acme/prod/common.tf      # -> ../common.tf
readlink terraform-acme/prod/variables.tf   # -> ../variables.tf

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

## Maintenance, repair & upgrade

This skill owns the artifacts for their whole life, not just day one. **If
`terraform-<tenant>/` or any `<tenant>-<component>` umbrella already exists, do
not stop — enter maintenance mode** and reconcile the existing tree to the
target erun version in place. First scaffold and ongoing maintenance are the
same skill.

**Target version.** Same resolution as Step 1: the env's `runtimeversion`
(which moves with `erun upgrade`), or an explicit version the operator names.
One erun version pins every reference on both sides — the Terraform `?ref` and
every Helm chart `version` — so bump them together, never piecemeal.

**Detect, then reconcile against this skill's own contract:**

- **Repair gaps** — fill what the contract requires but the tree lacks: absent
  `common.tf`/`variables.tf` symlinks, a missing per-env `values.<env>.yaml`
  (including `values.local.yaml` for the agent env), a missing
  `**/charts/*.tgz` entry in the repo `.gitignore`, an uncommitted
  `Chart.lock`. Add only what's missing; never clobber the project's own
  content (env-specific tfvars, values overrides, extra tenant modules).
- **Upgrade the pins** — re-pin **every** erun reference to `<ref>`/`<version>`:
  the terraform module `source = "…?ref=v<version>"` in each
  `modules/terraform-<tenant>-*/main.tf`, and each `<tenant>-<component>`
  umbrella `Chart.yaml` dependency `version:`.
- **Refresh derived artifacts** — after re-pinning, run `helm dependency update`
  on each umbrella to regenerate `Chart.lock` + `charts/` for the new versions,
  and commit the updated `Chart.lock`. (`helm dependency build` only rebuilds
  `charts/` from an *in-sync* lock and errors on a stale one — it fits a fresh
  scaffold with no lock and `erun deploy`'s pre-install step, not a re-pin.) Then
  re-apply terraform (`erun terraform apply`, Step 4) so the tree carries the
  upgraded module.
- **Clean up what the reconcile supersedes** — after previewing, remove only what
  no longer belongs: a `<tenant>-<component>` umbrella dir dropped from the plan
  (repo side), and in the env's namespace a deployed release the new set replaces —
  a hand-deployed release whose name doesn't match the `<tenant>-<component>`
  convention (e.g. `erun-backend-api`, now owned by the umbrella as
  `acme-backend-api`), or a component release no longer selected (`helm
  --kube-context <ctx> -n <ns> list` finds them). Uninstall a superseded release so
  the new one can adopt its resources — **but only when it is stateless.** A
  stateful release (postgres, or anything owning a data PVC) must not be casually
  `helm uninstall`ed: that can delete the PVC and the data with it (a chart-templated
  PVC — like postgres's — is removed on uninstall). Preserve the volume
  (retain it, let the new release adopt it) or **stop and flag it for explicit
  operator action** — never drop data, `/home/erun`, or a Secret as a side effect of
  a rename. When in doubt, leave it and report it.

**Idempotent, in-place, preview first.** Safe to re-run; edit files where they
live; **show the diff/plan before writing** — the pin changes and the files
you'd add — and let the operator confirm. Touch only version pins and genuine
gaps; never rewrite working project content, and never regenerate a file just
to reformat it.

**Confirm the tenant on a loose match.** If the existing `terraform-<tenant>/`
looks unrelated to the tenant you were asked about, confirm before reconciling
rather than assuming.

Maintenance does not relax the exclusions: still **never** wrap the runtime
`erun-devops` chart or add an `erun-devops`/`<tenant>-devops` umbrella (see What
this is not), and the runtime chart still deploys on its own with an empty
selection.

## Error behaviour

| Failure mode | Recovery |
|---|---|
| `terraform-<tenant>/` already exists | Not a stop — enter maintenance mode (see Maintenance, repair & upgrade): reconcile and upgrade the existing tree in place — re-pin every erun reference to the target version, fill gaps against the contract, refresh derived artifacts — after showing the diff/plan first. Offer to add a new `<env>/` folder if that's the ask. Confirm the tenant first only when the match looks unrelated. |
| The erun version can't be resolved (no `erun version`, no `runtimeversion`) | Stop and ask which erun version to pin to. Never default to `main` for production wiring — the edge and charts must match the deployed platform. |
| `terraform-erun-cluster-edge?ref=v<ver>` doesn't resolve on `terraform init` | The version has no matching git tag yet. Confirm the erun release exists; pin to the latest released `vX.Y.Z`. |
| `helm dependency build` 404s on the OCI chart | That version's chart isn't published. `erun push`/`erun release` publishes image + chart together — pin to a version that has been pushed. |
| `erun deploy` fails: `values file not found for environment "<env>": …/<component>/values.<env>.yaml` | That umbrella chart has no per-env values file for the env being deployed. Create `<component>/values.<env>.yaml` (an empty/comment-only file is valid). Remember the agent env: `values.local.yaml` is required too, since the desktop deploys `<tenant>-local`. |
| Operator asks to put the Cloudflare token in `<env>.tfvars` | Refuse. The token is a secret injected as `TF_VAR_cloudflare_api_token` from `CLOUDFLARE_API_TOKEN` at apply time — it must not be committed. |
| An `erun-devops`/`<tenant>-devops` umbrella appears under `<tenant>-devops/k8s/` | That is the runtime chart — `erun-build-env`'s concern, not this skill's. Don't create or edit it here. A `<tenant>-devops` chart is legitimate (and **required** once the tenant ships its own components, so the runtime rides the tenant version line) and owned by `erun-build-env`; leave it to that skill. Only remove a stray one this skill created by mistake — `erun deploy` matches the runtime release name and would otherwise install a duplicate as the runtime chart. |

## Important

- **Give the repo root agent guidance.** If the repository root has no
  `AGENTS.md`/`CLAUDE.md`, also apply the `erun-blueprint-agents` skill so any
  agent — or human — landing in the repo gets erun-environment orientation.
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
