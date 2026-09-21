---
title: erun jobs
---

# `erun jobs`

The platform's record of work **in flight**: what agents and orchestrators are working on right now, and what recently finished. Builds and gate runs are reported *after* the work completes; a job is claimed *before* it starts, which is the half that lets two actors see each other and stop duplicating work.

Four subcommands: `list`, `show`, `start`, and `finish`. This is the host-side counterpart to `erun exec job`, which starts and observes work inside one environment — `erun jobs` observes the tenant's whole queue, including work that never entered an environment at all.

## Synopsis

```
erun jobs list [flags]
erun jobs show JOB_ID [flags]
erun jobs start [flags]
erun jobs finish JOB_ID [flags]
```

Every subcommand accepts `--erun-alias` (defaults to the sole configured erun-type alias when only one is set up), `--dry-run` (trace the resolved HTTP call without sending it), and the global `--output json` for structured results.

## Subcommands

### `jobs list` {#jobs-list}

Lists jobs, the live queue first, narrowed by any combination of `--status`, `--environment-id`, `--issue`, `--scope`, and `--actor`. Each entry names what is being done, by whom, and how long it has been going.

A `RUNNING` job is work in flight. `ABANDONED` means its actor stopped updating it and the platform swept it — read it as dropped, not as failed. An empty queue is `[]`, never `null`.

### `jobs show` {#jobs-show}

Shows one job in full by JOB_ID, including the scope it claims and the in-pod job id it mirrors, when there is one.

### `jobs start` {#jobs-start}

Records that this actor is starting a piece of work: `--type`, `--summary`, and `--actor` are required; `--actor-kind`, `--environment`, `--issue`, `--scope`, and `--local-job-id` are optional.

With `--scope` set this is a **claim**. If an open job already holds that scope, the command fails with `409` naming the holder — its actor, its prose summary, and when it started — so you can pick up something else instead of duplicating the work.

`--type` is one of `fix`, `review`, `gate`, `release`, `deploy`, `investigate`, `plan`, `triage`, or `maintenance`; an unknown value is refused rather than stored, so the queue can group without a free-text bucket. The `--summary` is prose describing the work, never the command that performs it — a summary that is only a shell command is refused, and the pod's own job record keeps the command line as technical detail.

### `jobs finish` {#jobs-finish}

Reports how a job ended (`--status SUCCEEDED|FAILED|ABANDONED|SUPERSEDED`), refreshes what it is doing (`--summary`), or records the in-pod job id it mirrors (`--local-job-id`). A job that has already finished cannot be updated: its outcome is the record that coordination and reporting both read.

## Examples

```bash
erun jobs list --status RUNNING
erun jobs list --issue sophium/erun#2109
erun jobs start --type fix --issue sophium/erun#2109 --scope sophium/erun#2109 \
  --summary "fixing the jobs claim race" --actor erun/code4
erun jobs finish job_01H... --status SUCCEEDED
```

## Error behaviour

| Failure | Behaviour |
|---|---|
| No erun-type alias configured. | Aborts naming `erun cloud init erun --api-url <url>` as the fix. |
| More than one erun-type alias configured, `--erun-alias` omitted. | Aborts asking for an explicit `--erun-alias`. |
| JOB_ID does not exist, or belongs to another tenant (`show`, `finish`). | `404 Not Found`. |
| `--scope` is already held by an open job (`start`). | `409` naming the holder, what it is doing, and since when. |
| `--status` is a value outside the vocabulary (`finish`). | Aborts before sending anything. |
| `--type` is outside the vocabulary, or `--summary` is only a shell command (`start`). | `400`, naming the field. |

## Claiming is advisory

The scope claim is server-side coordination, not a distributed lock: two claims landing at the same instant can both be recorded. Making a scope exclusive in the database would wedge it permanently the moment an actor disappeared without closing its job. What the queue gives an operator is that overlap made visible, and a sweep closes a `RUNNING` job whose actor stopped updating it so a vanished actor cannot hold a scope forever.

## See also

- [MCP overview § Jobs](/mcp/overview#jobs) — the `jobs_list`/`jobs_show`/`jobs_start`/`jobs_finish` tool spec.
- `erun exec job` — starting and observing work inside one environment (not yet covered by a page of its own).
- [`erun gate list`](/cli/gate#gate-list) — the same queue view for the merge gate, whose records are reported after the fact rather than claimed before.
