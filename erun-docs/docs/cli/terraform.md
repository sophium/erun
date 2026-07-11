---
title: erun terraform
---

# erun terraform

Run a hosted platform's per-environment Terraform without hand-running `terraform` or `cd`-ing into a folder. `erun terraform` is for [platform deployments](/deployment/deploy-platform) whose Terraform is laid out per environment — one folder per env under `terraform-<tenant>/`, scaffolded by the [`erun-blueprint-platform`](/agent-reference/skills-spec#erun-blueprint-platform) skill. erun resolves the env's root from the current scope — `terraform-<tenant>/<environment>/` at the project root, or `<tenant>-devops/terraform-<tenant>/<environment>/` when the tenant keeps its whole devops footprint (`docker/`, `k8s/`, `terraform-<tenant>/`) under `<tenant>-devops/` (the same `-devops` convention `build`/`deploy` use) — picks up the symlinked `common.tf`, and runs that env's own `main.tf` with its `<environment>.tfvars`. The `terraform-<tenant>` base is the default; relocate it with [`paths.terraform`](/reference/configuration#paths-block) in `.erun/config.yaml` (erun still appends `/<environment>`).

Terraform's mutable artifacts live **off** the playbook tree: erun keeps the local-backend state file, the plan file, and `TF_DATA_DIR` (downloaded providers/modules) under `~/.erun/terraform/<tenant>/<environment>/` on the durable home directory (the `/home/erun` PVC in a runtime pod). State and the provider cache therefore survive a pod restart, while the image-baked playbooks stay read-only. The one file Terraform insists on keeping next to `main.tf` is `.terraform.lock.hcl` — `erun terraform init` generates it (you commit it) so a read-only runtime env never has to write it.

```bash
erun terraform init frs prod               # once: download providers + record the provider lock
erun terraform apply frs prod              # fmt → plan → confirm → apply
erun terraform plan frs prod               # read-only: plan
erun terraform destroy frs prod            # plan a destroy → confirm → apply
erun terraform apply frs prod --dry-run    # preview the resolved terraform commands
```

With no `TENANT`/`ENVIRONMENT` arguments, the command resolves the configured default scope. Run `init` once per environment (and again after you change providers) before `plan`/`apply`/`destroy` — they no longer init implicitly, matching how you run Terraform by hand.

## What it does

`TF_DATA_DIR` is set to `~/.erun/terraform/<tenant>/<env>/data` for every step, so downloaded providers and modules persist on the durable home directory across runs and restarts.

Run `erun terraform init <tenant> <env>` **once** first (and again after you change providers). It points the local backend at the durable state file (`-backend-config=path=~/.erun/terraform/<tenant>/<env>/terraform.tfstate`) and downloads providers into `TF_DATA_DIR`:

- On a **writable** checkout it generates `.terraform.lock.hcl` and records provider hashes for `linux/amd64` and `linux/arm64` (erun's deploy targets, via `terraform providers lock`). **Commit that lock** so it bakes into the runtime image — then a read-only runtime env can initialize from it. A single committed lock works on every env because it covers both deploy architectures.
- On a tree that **already has a committed lock** it runs `terraform init -lockfile=readonly`, refreshing only the PVC provider cache and never rewriting the (read-only) tree.

`erun terraform apply <tenant> <env>` then resolves the env's root (see above) and runs Terraform **in that folder**, so the env picks up its symlinked `common.tf` and adds its own services via `main.tf` — with no implicit init:

1. `terraform fmt -check -recursive ..` — **verifies** formatting across the tree without rewriting it (the playbooks are read-only in a runtime release); a formatting drift fails the step.
2. `terraform plan -input=false -var-file=<env>.tfvars -out ~/.erun/terraform/<tenant>/<env>/apply.tfplan` — using the env's var file when present.
3. **Confirm:** you are prompted to **type the environment name** before anything is applied — the guard against applying to the wrong environment. A wrong or empty entry aborts before the apply, so nothing is applied unless the name matches.
4. `terraform apply -input=false ~/.erun/terraform/<tenant>/<env>/apply.tfplan` — applies the exact plan you reviewed.

`plan` stops after step 2 (read-only — no `fmt`, no `-out`, no confirm). `destroy` plans a `-destroy` and applies it behind the same confirm gate. All three assume `init` has already run; if it hasn't, Terraform stops with a "run terraform init" error.

When the env has a Cloudflare alias, its `CLOUDFLARE_API_TOKEN` is forwarded to Terraform as `TF_VAR_cloudflare_api_token` (so the edge module's cert-manager DNS-01 solver can prove control of the zone). The token rides in the environment — it never appears in the command line or the trace.

## Flags

| Flag | Description |
|---|---|
| `--tenant <name>` | Target a specific tenant (defaults to the current scope). |
| `--environment <name>` | Target a specific environment (defaults to the tenant's default). |
| `--confirm-environment <env>` | Restate the environment name to confirm, bypassing the interactive prompt — for non-interactive use. `apply`/`destroy` only. |
| `--dry-run` | Resolve and print the full sequence of `terraform` commands without running any of them. |

## Error behaviour

| Condition | What happens | Recover |
|---|---|---|
| Not in a project (no git repo found on the host) | Aborts: `cannot find git project`; exit 1. | Run from inside your project checkout. This doesn't occur inside a runtime pod, where erun resolves the project tree automatically even though it has no `.git`. |
| No Terraform root at either candidate | Aborts: `no Terraform root for <tenant>/<env> — looked under terraform-<tenant>/<env>/ and <tenant>-devops/terraform-<tenant>/<env>/ …`; exit 1. | Scaffold the per-env root with [`erun-blueprint-platform`](/agent-reference/skills-spec#erun-blueprint-platform), or create it at either location with its `main.tf` and `<env>.tfvars`. |
| Environment not configured | Surfaces the config load error; exit 1. | Create the env (`erun init`) or fix the tenant/env name. |
| No default tenant/environment and none passed | Aborts: default tenant/environment not configured; exit 1. | Pass `TENANT ENVIRONMENT` (or set a default scope). |
| Confirmation doesn't match the env name | Aborts before the apply step: `confirmation "…" does not match environment "…"; aborting <operation>`; exit 1. The earlier read-only steps (`plan`, plus `fmt` for `apply`) have already run; only the apply is gated. | Re-run and type the exact environment name (or pass `--confirm-environment <env>`). |
| Providers not initialized (`init` not run) | A `plan`/`apply`/`destroy` `terraform` step stops with Terraform's *"Module not installed / please run terraform init"* error; exit 1. | Run `erun terraform init <tenant> <env>` first — once per environment, and again after changing providers. |
| `init` on a read-only tree with no committed lock | `erun terraform init` aborts before running Terraform: the playbook tree is not writable and has no `.terraform.lock.hcl` to init read-only from; exit 1. | Run `erun terraform init` on a writable env (e.g. `<tenant>-local`), commit the generated `.terraform.lock.hcl`, and rebuild/redeploy so it bakes into the image. |
| A `terraform` step fails (live run) | Surfaces the underlying `terraform` error and stops at that step; exit 1. | Fix the reported Terraform/cloud issue and re-run — `init`/`plan`/`apply` are safe to repeat. |

## See also

- [Deploying the platform](/deployment/deploy-platform) — where this fits in the platform bootstrap.
- [`erun-blueprint-platform`](/agent-reference/skills-spec#erun-blueprint-platform) — scaffolds the `terraform-<tenant>/` tree this command runs.
- [`erun-enable-hosting-edge`](/agent-reference/skills-spec#erun-enable-hosting-edge) — the guided skill that stands up the Cloudflare hosting edge.
