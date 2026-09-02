---
title: erun gate
---

# `erun gate`

The queue view [merge queue § Watching the gate](/collaboration/merge-queue#watching-the-gate) asks for: what is being gated right now, what is waiting, and what did the last gates decide — without knowing any job id, and independent of whether the change gated is an erun review or a plain branch (e.g. a repository whose changes arrive as GitHub pull requests). Two subcommands: `list` and `show`. Reporting a gate run's start and outcome is [`erun exec gate-run`](/cli/exec#exec-gate-run-start), not here — this is the read side.

## Synopsis

```
erun gate list [flags]
erun gate show GATE_RUN_ID [flags]
```

Every subcommand accepts `--erun-alias` (defaults to the sole configured erun-type alias when only one is set up), `--dry-run` (trace the resolved HTTP call without sending it), and the global `--output json` for structured results.

## Subcommands

### `gate list` {#gate-list}

Lists gate runs, most recent first, narrowed by any combination of `--target-branch`, `--source-branch`, and `--status`. Each entry names the branch, the prospective merge commit actually tested, the target, and the verdict — and, for a `FAILED` one, which gate step failed and where to read it.

A `RUNNING` entry is being gated right now. `INCONCLUSIVE` means the gate never reached a real verdict — a wrapper timeout, an environment fault — read it as unresolved, not as a failure.

### `gate show` {#gate-show}

Shows one gate run in full by GATE_RUN_ID.

## Examples

```bash
erun gate list --target-branch main
erun gate list --status FAILED
erun gate show abc123
```

## Error behaviour

| Failure | Behaviour |
|---|---|
| No erun-type alias configured. | Aborts naming `erun cloud init erun --api-url <url>` as the fix. |
| More than one erun-type alias configured, `--erun-alias` omitted. | Aborts asking for an explicit `--erun-alias`. |
| GATE_RUN_ID does not exist, or belongs to another tenant (`show`). | `404 Not Found`. |

## See also

- [Merge queue § Watching the gate](/collaboration/merge-queue#watching-the-gate) — why this exists and how a gate run relates to a review's own `GATE` build.
- [MCP overview § Gate runs](/mcp/overview#gate-runs) — the `gate_list`/`gate_show`/`exec_gate-run-*` tool spec.
- [`erun exec gate-run start` / `erun exec gate-run report`](/cli/exec#exec-gate-run-start) — the write side that makes a gate run visible here.
- [`erun review`](/cli/review) — the review-driven half of the merge queue.
