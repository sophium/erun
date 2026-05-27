---
title: Workspace sync spec
---

# Workspace sync spec

> For the Operator view, see [Desktop app · Workspace sync](/desktop/overview).

The desktop's workspace-sync feature mirrors a local folder into the runtime pod. This is **not** how the project repo is shared — the repo is mounted (`local-agent`) or PVC-checked-out (`remote-agent`). Workspace sync covers ancillary files outside the repo: scratch notes, editor temp files, secrets the Operator wants in `~` inside the pod without committing.

## Configuration

| Field | Type | Default | Effect |
|---|---|---|---|
| `EnvConfig.sshd.workspacesync.enabled` | bool | `false` | When `true`, the desktop spawns the sync poller for this env after `erun open`. |
| `EnvConfig.sshd.workspacesync.localpath` | string (absolute path) | unset | The local folder to mirror. Required when `enabled: true`. |

When `enabled` is `true` and `localpath` is unset, `erun open` logs `WORKSPACE_SYNC_NO_LOCALPATH` and proceeds without the sync.

## Mapping

| Local source | Pod destination |
|---|---|
| `<localpath>/<relative>` | `/home/erun/sync/<relative>` |

`/home/erun/sync/` is created on first sync; the directory persists in the env's PVC, so files survive pod restarts.

## Polling algorithm

1. The poller starts after `erun open` reports SSH readiness.
2. Every **2 seconds**, the poller walks `<localpath>` and computes a per-file `{mtime, size}` tuple.
3. For each file whose tuple differs from the last-seen state: the poller `scp`'s the file (over the env's SSH transport) to `/home/erun/sync/<relative>` inside the pod.
4. New files are added on the next tick. Modified files are overwritten. Deleted files locally are **not** propagated to the pod (see Cleanup below).
5. The poller exits when the desktop closes the env, or when the env's runtime pod is unreachable for 30 consecutive ticks (1 minute).

The sync is **one-way only**: local → pod. Edits made inside `/home/erun/sync/` on the pod are not pulled back to the local source. This is deliberate — the contract is "local is authoritative for ancillary files; the repo is authoritative for code."

## Cleanup

A side process inside the pod (the `erun-devops` container's `workspace-sync-sweeper` goroutine) removes files under `/home/erun/sync/` that:

1. Are older than **24 hours** (mtime). And
2. Have no matching local source — verified by asking the desktop's sync poller (over MCP) "is `<relative>` still in `<localpath>`?". The query is cached for 5 minutes.

The sweeper runs every hour. A file briefly absent locally (editor temp-rename, brief delete-recreate) does not disappear from the pod immediately — the 24-hour mtime gate buffers it.

## Constraints

| Constraint | Reason |
|---|---|
| Total `<localpath>` size ≤ 200 MiB | The poller computes the full file tree on every tick; large trees blow the budget. |
| File count ≤ 5000 | Same reason. |
| Per-file size ≤ 50 MiB | Per-tick `scp` runtime stays bounded. |
| `<localpath>` must be on a local filesystem (not a network mount) | mtime semantics are unreliable on NFS / SMB. |

Violations surface as a desktop warning; the sync continues with whatever fits the budget.

## Failure modes

| Symptom | Cause | Recovery |
|---|---|---|
| New local files don't appear in the pod | Sync poller is not running (env was opened before `workspacesync.enabled` was set). | Re-open the env. |
| Files appear in the pod but stale ones never go away | Cleanup sweeper is paused (pod restart wiped state). | `kubectl exec` into `erun-devops`, run `find /home/erun/sync -mtime +1 -delete` once. |
| Sync poller exits silently | Pod unreachable for 30 ticks. | `erun doctor`; re-open the env. |
| Large file fails to copy | Exceeds per-file size budget. | Reduce file size, or commit it to the repo instead. |

## See also

- [Desktop app · Workspace sync](/desktop/overview) — Operator-facing summary.
- [Networking spec · Port-forward state files](/agent-reference/networking-spec) — the SSH transport this sync rides on.
- [Inside an environment](/concepts/runtime-pods) — what else lives in `/home/erun/`.
