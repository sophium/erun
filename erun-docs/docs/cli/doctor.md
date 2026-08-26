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

For an environment carrying an AWS cloud alias, `doctor` also reports the **host AWS credentials** it acts with: whether the pod's `erun-host` profile exists, when it expires (or that it has already **expired**), and the AWS region the environment resolves — or that none does. Both failures otherwise surface far from their cause, as an SDK `ExpiredToken` or an image pull rejected with `no basic auth credentials`, so this is the check that names them. The fix in either case is [`erun cloud refresh`](/cli/cloud#cloud-refresh); see [Troubleshooting](/reference/troubleshooting#host-credentials-expired). Reading it requires the runtime pod: when the pod isn't reachable, `doctor` reports **could not read** for this check instead of aborting, pointing back at the helm release status and pod state it already reported above, and carries on to the rest of the run — retry the check once the pod is running again.

`doctor` also compares the environment's `runtimeimage` against its `runtimeregistry` and flags a mismatch by name — a runtime image pinned to a different registry than the one credentials refresh for is exactly the split that leaves a redeployed pod stuck in `ImagePullBackOff`. This check reads only local config, so it runs identically whether or not the pod is up. On a mismatch it names both registries and points to two fixes: confirm a credential for the image's registry resolves where `erun deploy`/`erun open --deploy` runs, or realign the two with `erun init <tenant> <env> --runtime-registry <registry>` so `runtimeregistry` matches the image. For example:

```
== Runtime image registry ==
runtimeimage resolves to registry 123456789012.dkr.ecr.eu-west-2.amazonaws.com, but runtimeregistry is ghcr.io/sophium.
The deploy that installs this env sets imageOverrides.erun-devops from 123456789012.dkr.ecr.eu-west-2.amazonaws.com while runtimeRegistry stays ghcr.io/sophium; the runtime pod can only pull if a credential for 123456789012.dkr.ecr.eu-west-2.amazonaws.com also resolves where `erun deploy`/`erun open --deploy` runs (the same AWS/docker session that can push to it). If the pod is failing to pull, confirm that credential is available there, or realign the two with `erun init team dev --runtime-registry 123456789012.dkr.ecr.eu-west-2.amazonaws.com` to match the image.
```

See [Troubleshooting](/reference/troubleshooting#runtime-image-registry-mismatch).

When any item is `missing`, `doctor` offers to run the corresponding recovery step.

## What it can repair

Beyond reporting, `doctor` offers these fixes (each prompts first, or runs non-interactively with its flag):

- **Deploy recovery** — when the diagnosis shows the runtime release is unhealthy, `doctor` recommends the **one** recovery that fits: clearing a stuck pending helm release when a deploy died mid-upgrade and left it locked, or rolling back to the last successful revision when the current one is bad. It prompts for that single action (never both — they are alternative fixes, and running both would roll the release back a revision too far). These mutate the live release and are offered only when the release looks unhealthy, never on a healthy env. To rebuild and roll out fresh images instead, re-run `erun deploy --force`.
- **Docker cleanup** — prune the environment's unused images, build cache, or stopped containers. These run against the environment's Docker, not your laptop's.
- **Root config repair** — restore the root erun config from a dated backup, or re-initialize orphaned cloud provider aliases.
- **Environment config restore** — restore one environment's `config.yaml` from a dated backup when a setting was changed or corrupted (for example an environment type that resolved to the wrong value). Each save snapshots the previous config alongside it, so there is a daily backup to roll back to.
- **JetBrains Gateway** — clear cached backend metadata for the environment when a Gateway connection is stuck.
- **Workspace sync (host mirror)** — for a remote-agent env with workspace sync enabled, report the host mirror's SSH provisioning and, with `--repair-workspace-sync`, repair it **without redeploying**: resolve/persist the SSH key, write the local `~/.ssh/config` alias, install the pod's `authorized_keys` through the runtime container (not a helm redeploy), and ensure the SSH port-forward. If SSH still can't reach the pod afterwards, it names `erun sshd init` as the remaining step (the redeploy this repair won't run).
- **Remote init** — inside a runtime pod, finish an interrupted init (SSH keygen, repo clone).

The exact flags for running these non-interactively are on the [CLI flag spec](/agent-reference/cli-flags#erun-doctor).

## Flags

| Flag | Description |
|---|---|
| `--dry-run` | Run the inspection and print the recovery plan without performing any recovery actions. |
| `--sync-config` | Reconcile the in-pod erun config with the helm-injected `ERUN_*` env vars. Only takes effect inside a runtime pod. The injected env wins: erun rebuilds the canonical projection (`type`, `kubernetescontext`, `cloudprovideralias`, `managedcloud`, the cloud-context/provider blocks, `idle`, `runtimeregistry`, `containerregistries`, `disablebuildscript`) and rewrites those keys, **preserving** every key the env does not carry (`sshd`, `claude`, `runtimeversion`, `localrepopath`, …). Drift is reported per key as `missing`, `wrong`, or `legacy`; with `--dry-run` the file writes are traced but not performed. |
| `--restore-env-config-from-backup <date\|path>` | Restore the target environment's `config.yaml` from a dated backup (`YYYY-MM-DD`) or an absolute path, before the rest of the inspection runs so a corrupted env config can be recovered first. Needs an explicit tenant and environment. With `--dry-run`, the copy is traced but not performed. |
| `--repair-workspace-sync` | For a remote-agent env with workspace sync enabled, repair the host mirror's SSH provisioning without redeploying the runtime: resolve/persist the SSH key, write the local `~/.ssh/config` alias, install the pod's `authorized_keys` through the runtime container, and ensure the SSH port-forward. With `--dry-run`, every action is traced and nothing runs. |

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
| `--restore-env-config-from-backup` without an explicit tenant and environment. | Aborts with `--restore-env-config-from-backup needs an explicit tenant and environment`; exit code 1; nothing is changed. |
| `--restore-env-config-from-backup <date>` with no matching backup. | Aborts naming the unmatched selector and the target env (`no env config backup matches "<date>" for <tenant>/<env>`); exit code 1; nothing is changed. |
| Helm release missing or cluster unreachable during deploy diagnosis. | The helm/pod probe output (including the error) is shown as part of the diagnosis and `doctor` continues; the diagnosis is read-only, so nothing is changed. |
| Host AWS credentials or docker-storage check needs the runtime pod, but it isn't reachable. | `doctor` reports `could not read` for that check, points back at the helm release status and pod state already shown, and continues the rest of the run instead of aborting. Any requested prune action (`--prune-images`, `--prune-build-cache`, `--prune-containers`) is skipped with the same reason. |
| Deploy recovery (`--clear-pending-helm` / `--rollback`) fails. | The failing helm/kubectl output is surfaced and `doctor` aborts with that error; the release is left as helm leaves it (a failed rollback does not partially apply). Re-run the diagnosis to see the new state, then retry or `erun deploy --force`. |
| `--rollback` with no prior successful revision. | `helm rollback` reports it has no revision to roll back to; nothing changes. Use `--clear-pending-helm` then `erun deploy --force` instead. |
| Both `--clear-pending-helm` and `--rollback` passed. | Aborts immediately with `--clear-pending-helm and --rollback are alternative recoveries; pass only one`; exit code 1; nothing runs. |
| Prune not confirmed and no `--prune-*` flag. | No Docker state is touched — prunes run only on confirmation or with the matching flag. |
| Run inside a runtime pod with a complete init. | Reports "nothing to finish" and exits 0. |
| Cluster unreachable. | Reports the pod check as failed; config and workspace checks still run. |
