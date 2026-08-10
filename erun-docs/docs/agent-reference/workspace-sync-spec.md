---
title: Workspace sync spec
---

# Workspace sync spec

> For the Operator view, see [Desktop app · Workspace sync](/desktop/overview) and [`erun sshd sync`](/cli/sshd).

Workspace sync mirrors a `remote-agent` environment's **pod worktree onto the host**, one way, as plain files. It is what gives an operator or a host-side orchestrator a local, readable copy of code that only exists inside the pod — the review surface — plus the artifacts the pod cross-built for this host.

It is **not** how the repo reaches the pod. A `local-agent` env's worktree is hostPath-mounted and a `remote-agent` env's is checked out into its PVC; sync neither establishes nor affects that. Nothing a host writes into the mirror travels back: the mirror is a copy, and an edit there is overwritten by the next pass.

## Configuration

| Field | Type | Default | Effect |
|---|---|---|---|
| `EnvConfig.sshd.enabled` | bool | `false` | Sync rides the env's SSH channel, so SSHD must be enabled. |
| `EnvConfig.sshd.workspacesync.enabled` | bool | `false` | Opts the environment into being mirrored. |
| `EnvConfig.sshd.workspacesync.localpath` | string (absolute path) | unset | The host directory that receives the mirror. Required when `enabled: true`. |

## Direction and mapping

| Pod source | Host destination |
|---|---|
| `<remote worktree>/<relative>` | `<localpath>/<relative>` |
| `$ERUN_OUTPUTS_DIR/<relative>` | `<localpath>/.erun-outputs/<relative>` |

The mirror carries no git directory of its own — it is a plain directory copy, so the authoritative diff of uncommitted work is still taken in the pod.

`.erun-outputs/` is the artifacts lane. Artifacts live outside the worktree, so they escape the repo's ignore rules and reach the host even when the source lane would skip them — this is how a Windows binary cross-built in a Linux pod gets to a host that can run it. Its files are written read-only, and the source lane skips the directory, so the two lanes never contend.

`.erun-sync-staging/` is where a pass lands bytes that are still arriving; see [Atomic publish](#atomic-publish). Both `.erun-outputs/` and `.erun-sync-staging/` are reserved names: a pod file at either path is not mirrored.

## One pass

1. List the pod's **git-visible** files (`git ls-files --exclude-standard`). The pod applies the repo's ignore rules, so the host needs no git of its own to know what belongs in the mirror.
2. Subtract index entries whose file the pod's worktree no longer has. `git ls-files -c` reports the index, not the worktree, so without this an unstaged deletion never reaches the host and the mirror keeps the file forever.
3. Subtract symlinks. They cannot round-trip into a plain-directory mirror on every host; the target file still syncs as a regular file, so only the redundant pointer is lost.
4. Walk `<localpath>` for the mirror's own `{size, mtime}` fingerprints.
5. Fetch only the files whose fingerprint differs, streamed as a tar over the SSH channel into `<localpath>/.erun-sync-staging/`, then rename each one onto its final path. `tar` preserves mtime and `rename` keeps it, so an unchanged file matches next pass and a steady state costs one metadata listing rather than a whole-tree transfer.
6. Delete mirror files the pod's listing omits, then prune the directories that leaves empty.
7. Mirror the outputs directory into `.erun-outputs/` and prune artifacts the pod no longer has.

A fetch failure does **not** strand step 6: deletion correctness depends only on the pod's listing, not on whether every changed file transferred. One un-fetchable file must never block every deletion.

Each pass emits one bounded log line with its own inputs and counts — never one line per file.

## Atomic publish

A file appears at its path in the mirror only once it is whole. Both lanes fetch into `<lane>/.erun-sync-staging/` and then `rename` each file onto its final path, which is atomic on POSIX and replaces on Windows — so a reader opening a mirrored file gets either the previous version or the complete new one, never a prefix of one.

This is what makes the mirror usable as the verification surface. A truncated binary is not an error: it copies cleanly, reports a plausible size, and identifies as its own format, while a signature or trailer that lives at the end of the file is simply missing. Read as evidence, that is a wrong answer rather than a failed one.

Consequences an Agent can rely on:

| Property | Guarantee |
|---|---|
| Partial state | Only ever inside `.erun-sync-staging/`, never at a final path. The directory name is the marker; there is no "is this finished?" to guess at. |
| Staged bytes | Never mirror content: both lanes skip the directory, so it is never listed, fingerprinted, pruned, marked read-only, or offered as an artifact. |
| Failure, cancellation, or a killed pass | The mirror keeps its previous content, and the staging directory is removed. Debris a killed process leaves is cleared at the start of the next pass before anything is staged. |
| Peak disk | One staging copy of the files this pass is fetching, not of the whole tree. |

## Transports

The same pass is reachable three ways, and all three resolve the environment through one shared resolution, so they agree on what a pass addresses and on when there is nothing to address.

| Transport | Entry point |
|---|---|
| Desktop | A poller per enabled env, one pass every 2 seconds, started for every configured env at launch and on `erun open`. |
| CLI | [`erun sshd sync [TENANT] [ENVIRONMENT]`](/cli/sshd) — one pass. |
| MCP | The `workspace_sync` tool, served by `erun mcp proxy` on the host rather than by the in-pod edge, because the mirror is on the host. See [MCP · Host-served](/mcp/overview#host-served--the-review-mirror). |

## Refusals

A pass that cannot run says which precondition failed rather than reporting a no-op, because "the mirror did not change" is the one symptom they all share and the fix differs for each.

| Condition | Meaning |
|---|---|
| Environment is not `remote-agent` | There is no pod worktree to mirror. A `local-agent` env's worktree is already on this host. |
| Workspace sync not enabled | SSHD or `workspacesync.enabled` is off for this environment. |
| No local path | `workspacesync.localpath` is unset and no project root resolved. |
| SSH channel down | The env's SSH channel is not up; the port-forward `erun open` establishes is what makes it reachable. |

## Preview

The CLI's `--dry-run` and the MCP tool's `preview` run the same resolution and the same listings, then stop before every write: no mirror directory is created, nothing is fetched, nothing is deleted. They report the counts a real pass would have changed, so a preview is a measurement rather than a summary note.
