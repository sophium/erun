---
title: erun doctor
---

# `erun doctor`

Inspect the local ERun configuration or the runtime pod state, report why a deploy may have failed, and offer recovery actions for any problems it finds.

## Synopsis

```
erun doctor [TENANT] [ENVIRONMENT] [flags]
```

## What it checks

`erun doctor` runs a different check set depending on context. From your laptop, it validates the per-user tenant + env config, the kubeconfig context, the runtime pod's reachability, and the project root, and reports why a deploy may have failed: the helm release status and the runtime namespace's pods (read-only — it never touches the release). Inside the runtime pod (detected via `ERUN_REPO_REMOTE=true`) it inspects the bootstrap marker, the in-pod project root, the git checkout, the SSH keypair, and the CodeCommit RSA key when applicable.

When the deploy diagnosis shows a stuck pending release or a failed image pull, the fix is to re-run `erun deploy --force` (rebuild and redeploy) or clear the pending release — the desktop app offers both as one-click buttons on the failed deploy in its [Activities panel](/desktop/overview#control-panel).

For the full per-check id catalogue and the offered recovery actions, see [Agent reference · CLI flag spec · `erun doctor`](/agent-reference/cli-flags#erun-doctor).

When any item is `missing`, `doctor` offers to run the corresponding recovery step.

## What it can repair

Beyond reporting, `doctor` offers these fixes (each prompts first, or runs non-interactively with its flag):

- **Deploy recovery** — when the diagnosis shows the runtime release is unhealthy, recover it by clearing a stuck pending helm release (a deploy that died mid-upgrade leaves the release locked, blocking the next deploy) or rolling it back to its last successful revision. These mutate the live release, so each prompts first; they are offered only when the release looks unhealthy, never on a healthy env. To rebuild and roll out fresh images instead, re-run `erun deploy --force`.
- **Docker cleanup** — prune the environment's unused images, build cache, or stopped containers. These run against the environment's Docker, not your laptop's.
- **Root config repair** — restore the root erun config from a dated backup, or re-initialize orphaned cloud provider aliases.
- **JetBrains Gateway** — clear cached backend metadata for the environment when a Gateway connection is stuck.
- **Remote init** — inside a runtime pod, finish an interrupted init (SSH keygen, repo clone).

The exact flags for running these non-interactively are on the [CLI flag spec](/agent-reference/cli-flags#erun-doctor).

## Flags

| Flag | Description |
|---|---|
| `--dry-run` | Run the inspection and print the recovery plan without performing any recovery actions. |

## Examples

Run from your laptop against the effective env:

```bash
erun doctor
```

Run inside a runtime pod after an interrupted init:

```bash
# (SSH'd into the pod)
erun doctor                # see what's missing
erun doctor --dry-run      # preview what doctor would do to fix it
```

Exit codes and the meaning of each are spec'd in [Agent reference · CLI flag spec · `erun doctor` exit codes](/agent-reference/cli-flags#erun-doctor).

## Sample output

A healthy local-side run against an env named `local` on Docker Desktop:

```
erun doctor — my-tenant / local
  config:
    tenant config         ok  ~/.config/erun/my-tenant/tenant.yaml
    environment config    ok  ~/.config/erun/my-tenant/local/config.yaml
    project config        ok  /Users/you/code/my-project/.erun/config.yaml
  cluster:
    kubernetes context    ok  docker-desktop
    runtime pod           ok  my-tenant-local/erun-devops-7c8b6d (running)
  workspace:
    project root          ok  /Users/you/code/my-project (git repo)

all checks passed
```

An unhealthy run after an interrupted init:

```
erun doctor — my-tenant / rihards-dev
  config:
    tenant config         ok  ~/.config/erun/my-tenant/tenant.yaml
    environment config    ok  ~/.config/erun/my-tenant/rihards-dev/config.yaml
  cluster:
    kubernetes context    ok  erun-004-020362606330-eu-west-2
    runtime pod      missing  no pod found in namespace my-tenant-rihards-dev

  recovery actions:
    [1] deploy runtime chart (erun open my-tenant rihards-dev)

  run `erun doctor --dry-run` to preview the recovery, or `erun doctor` again with `-y` to apply.
```

The check format is fixed (`<category>: <name> <status> <detail>`); machine-readable consumers should prefer the [MCP `doctor` tool](/mcp/overview#doctor) which returns the same data as typed JSON.

## Error behaviour

| Failure | Behaviour |
|---|---|
| Root config missing or corrupted. | Reports it; with `--repair-config` offers to restore from a dated backup, otherwise leaves it untouched. |
| Helm release missing or cluster unreachable during deploy diagnosis. | The helm/pod probe output (including the error) is shown as part of the diagnosis and `doctor` continues; the diagnosis is read-only, so nothing is changed. |
| Deploy recovery (`--clear-pending-helm` / `--rollback`) fails. | The failing helm/kubectl output is surfaced and `doctor` aborts with that error; the release is left as helm leaves it (a failed rollback does not partially apply). Re-run the diagnosis to see the new state, then retry or `erun deploy --force`. |
| `--rollback` with no prior successful revision. | `helm rollback` reports it has no revision to roll back to; nothing changes. Use `--clear-pending-helm` then `erun deploy --force` instead. |
| Prune not confirmed and no `--prune-*` flag. | No Docker state is touched — prunes run only on confirmation or with the matching flag. |
| Run inside a runtime pod with a complete init. | Reports "nothing to finish" and exits 0. |
| Cluster unreachable. | Reports the pod check as failed; config and workspace checks still run. |
