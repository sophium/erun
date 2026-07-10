---
title: erun terraform
---

# erun terraform

Run a hosted platform's per-environment Terraform without hand-running `terraform` or `cd`-ing into a folder. `erun terraform` is for [platform deployments](/deployment/deploy-platform) whose Terraform is laid out per environment — one folder per env under `terraform-<tenant>/`, scaffolded by the [`erun-blueprint-platform`](/agent-reference/skills-spec#erun-blueprint-platform) skill. erun resolves `terraform-<tenant>/<environment>/` from the current scope, picks up the symlinked `common.tf`, and runs that env's own `main.tf` with its `<environment>.tfvars`. The `terraform-<tenant>` base is the default; relocate it with [`paths.terraform`](/reference/configuration#paths-block) in `.erun/config.yaml` (erun still appends `/<environment>`).

```bash
erun terraform apply frs prod              # init → fmt → plan → confirm → apply
erun terraform plan frs prod               # read-only: init → plan
erun terraform destroy frs prod            # plan a destroy → confirm → apply
erun terraform apply frs prod --dry-run    # preview the resolved terraform commands
```

With no `TENANT`/`ENVIRONMENT` arguments, the command resolves the configured default scope.

## What it does

For `erun terraform apply <tenant> <env>`, it resolves `terraform-<tenant>/<env>/` under the project root and runs Terraform **in that folder**, so the env picks up its symlinked `common.tf` and adds its own services via `main.tf`:

1. `terraform init -input=false`
2. `terraform fmt -recursive ..` — normalises the whole `terraform-<tenant>/` tree before planning, so HCL drift doesn't accumulate.
3. `terraform plan -input=false -var-file=<env>.tfvars -out apply.tfplan` — using the env's var file when present.
4. **Confirm:** you are prompted to **type the environment name** before anything is applied — the guard against applying to the wrong environment.
5. `terraform apply -input=false apply.tfplan` — applies the exact plan you reviewed.

`plan` stops after step 3 (read-only — no `fmt`, no `-out`, no confirm). `destroy` plans a `-destroy` and applies it behind the same confirm gate.

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
| No `terraform-<tenant>/<env>/` folder | Aborts: `no Terraform root at … — scaffold it with the erun-blueprint-platform skill …`; exit 1. | Scaffold the per-env root with [`erun-blueprint-platform`](/agent-reference/skills-spec#erun-blueprint-platform), or create `terraform-<tenant>/<env>/` with its `main.tf` and `<env>.tfvars`. |
| Environment not configured | Surfaces the config load error; exit 1. | Create the env (`erun init`) or fix the tenant/env name. |
| No default tenant/environment and none passed | Aborts: default tenant/environment not configured; exit 1. | Pass `TENANT ENVIRONMENT` (or set a default scope). |
| Confirmation doesn't match the env name | Aborts before the apply step: `confirmation "…" does not match environment "…"; aborting <operation>`; exit 1. The earlier read-only steps (`init`, `plan`, plus `fmt` for `apply`) have already run; only the apply is gated. | Re-run and type the exact environment name (or pass `--confirm-environment <env>`). |
| A `terraform` step fails (live run) | Surfaces the underlying `terraform` error and stops at that step; exit 1. | Fix the reported Terraform/cloud issue and re-run — `init`/`plan`/`apply` are safe to repeat. |

## See also

- [Deploying the platform](/deployment/deploy-platform) — where this fits in the platform bootstrap.
- [`erun-blueprint-platform`](/agent-reference/skills-spec#erun-blueprint-platform) — scaffolds the `terraform-<tenant>/` tree this command runs.
- [`erun-enable-hosting-edge`](/agent-reference/skills-spec#erun-enable-hosting-edge) — the guided skill that stands up the Cloudflare hosting edge.
