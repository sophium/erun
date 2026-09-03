---
title: Data retention
---

# Data retention

The `erun-backend-db` component runs a daily sweep that deletes old rows from a handful of high-growth tables (reviews, comments, releases, AI sessions, invites) so a tenant's database doesn't grow forever. It's fully automatic once `erun-backend-db` is deployed — most of the time there's nothing to do. This page is for the moments there is: previewing what a sweep would remove before it runs for real, checking or changing the bounds it enforces, and confirming what a scheduled run actually did.

Not to be confused with [registry image retention](/deployment/registries#hosted-registry) — a separate mechanism that expires unused container images, on its own schedule and its own configuration.

## What's enforced today, and what's still just a design

| Tables | Status |
|---|---|
| `reviews`, `review_reviewers`, `comments`, `releases`, `ai_sessions`, `invites`, `invite_requests` | **Enforced** — a daily sweep deletes eligible rows in every tenant running `erun-backend-db` |
| `builds`, `gate_runs` | **Designed, not enforced.** Rows accumulate with no bound today ([#1956](https://github.com/sophium/erun/issues/1956)) — porting the design onto the sweep below is the remaining work, not a technical blocker. |
| `audit_events`, `usage_events` | **Designed, not enforced — deliberately.** Whether erun has a compliance/contractual obligation on audit-log retention, and if so what window, is an unanswered business question ([#1959](https://github.com/sophium/erun/issues/1959)). Until it's answered, these two tables keep growing without limit on purpose: guessing a window and deleting an audit row that turns out to matter is worse than the storage cost of keeping it. |

## Previewing a sweep — dry run

Every policy file under `erun-backend-db/retention/` requires a `dry_run` variable and always reports what it would delete before it deletes anything — the delete itself only runs when `dry_run` is `false`. Run a file directly against the tenant's database to preview it, with no risk of it touching data:

```bash
psql -v ON_ERROR_STOP=1 -v dry_run=true \
  -f erun-backend-db/retention/reviews.sql \
  "$ERUN_DATABASE_URL"
```

This prints one row per table naming how many rows are eligible for deletion right now, and nothing else happens. Run it against each file to preview every enforced policy: `comments_releases.sql`, `reviews.sql`, `ai_sessions.sql`, `invites_invite_requests.sql`.

To preview the exact sweep the scheduled job runs — same image, same database credentials, same four files in order — without touching the running CronJob, clone it into a one-off Job with the dry-run switch flipped:

```bash
kubectl -n <tenant>-prod create job retention-dry-run \
  --from=cronjob/<tenant>-backend-db-retention \
  --dry-run=client -o yaml \
  | kubectl set env -f - --local ERUN_RETENTION_DRY_RUN=true -o yaml \
  | kubectl -n <tenant>-prod apply -f -

kubectl -n <tenant>-prod logs job/retention-dry-run -f
```

The clone carries over the CronJob's own `ERUN_DATABASE_URL`/postgres-password wiring, so the only thing the pipeline changes is the dry-run flag. Delete the Job when you're done reading its log — a one-off Job isn't cleaned up automatically:

```bash
kubectl -n <tenant>-prod delete job retention-dry-run
```

## Turning retention on and off

**On.** Retention ships bundled with the `erun-backend-db` component — there's no separate switch. Deploying that component installs the retention CronJob alongside the schema-migration Job:

```bash
erun deploy --version <version> --components erun-backend-db
```

If a tenant runs `erun-backend-db` at all, its retention sweep runs daily at 03:00 UTC. There's no way to deploy the component without it.

**Off.** There is no chart value for this yet — the retention CronJob renders unconditionally whenever `erun-backend-db` is deployed (the chart's own test suite locks this down explicitly). The lever available today is suspending the CronJob object directly:

```bash
kubectl -n <tenant>-prod patch cronjob <tenant>-backend-db-retention \
  --type merge -p '{"spec":{"suspend":true}}'
```

and to resume it:

```bash
kubectl -n <tenant>-prod patch cronjob <tenant>-backend-db-retention \
  --type merge -p '{"spec":{"suspend":false}}'
```

`spec.suspend` isn't part of the chart's own manifest, so a later `erun deploy`/`helm upgrade` of `erun-backend-db` won't reset it back to running — the suspension holds until you flip it again by hand. This is a stopgap, not a first-class feature: there's no `retention.enabled`-style value wired into the chart.

## Bounds per table, and where to change one

| Policy file | Table (scope) | Age bound | Count cap |
|---|---|---|---|
| `comments_releases.sql` | `releases` | 180 days | 1,000 / tenant |
| `comments_releases.sql` | `comments` (closed root threads only; a qualifying thread deletes with all its replies) | 30 days (from close) | 5,000 / tenant |
| `reviews.sql` | `reviews` (`CLOSED` only — a `MERGED` review is never touched) | 90 days | 2,000 / tenant |
| `reviews.sql` | `review_reviewers` | deleted together with its review; no bound of its own | — |
| `ai_sessions.sql` | `ai_sessions` (sessions whose last event is `exit`; a live session is never touched regardless of age) | 14 days | 500 / (tenant, environment) |
| `invites_invite_requests.sql` | `invite_requests` (`APPROVED`/`DECLINED` only; `PENDING` is never touched) | 180 days (from decision) | 10,000 platform-wide (no `tenant_id` on this table) |
| `invites_invite_requests.sql` | `invites`, consumed | 365 days (from consumption) | 10,000 / tenant |
| `invites_invite_requests.sql` | `invites`, expired and never consumed | 30 days (from expiry) | 10,000 / tenant (a separate cap from the consumed population, so a flood of one kind can't evict the other's history) |

Either bound prunes on its own — a row that's old enough or beyond the count cap is eligible regardless of the other. Bounds are literal SQL inside the `.sql` files (`interval '90 days'`, `rn > 2000`), not chart values or environment variables. Changing one means editing that file, then going through the normal release cycle for the `erun-backend-db` image — build, publish, and redeploy the component to the target tenant. There's no runtime-tunable knob today.

## What's lost when a row ages out

Every deletion below is a hard `DELETE` inside a transaction. There's no soft-delete, no archive copy, and no undo — once a sweep removes a row, it's gone.

| Rows | What's lost |
|---|---|
| Closed comment threads | The discussion itself. Only `CLOSED` threads are ever eligible, so no open conversation is at risk. |
| Releases | The record of what version a given commit produced. The uniqueness guard that stops the same commit from being released twice also disappears with the row — the git tag and published image outside the database are unaffected, but the database's own memory of "this commit was already released" is gone. |
| Closed, unmerged reviews | The review's history and every reviewer ever assigned to it (its `review_reviewers` rows are removed in the same transaction). A review that ever ran a gate build, still has comments or a release attached, or reached `MERGED` is left alone. |
| Exited AI sessions | The session's last known terminal state — how it ended. A session still in progress is never touched. |
| Consumed invites | The record of who was let into the tenant, by whom, and when. There's no second copy of this anywhere — accepting an invite doesn't write an audit-log entry. |
| Expired, never-consumed invites | Only "did we actually send invite X" — a debugging fact, not an access record. |
| Decided invite requests | The request's own content, and for a decline, the only record of *why* — `decline_reason` has no copy elsewhere. |

`builds`, `gate_runs`, `audit_events`, and `usage_events` are not swept at all today (see the status table above), so nothing is lost from them yet — they simply keep growing.

## Confirming a scheduled run happened

The CronJob fires daily at 03:00 UTC; overlapping runs are forbidden, so two days' sweeps never race each other's deletes.

```bash
kubectl -n <tenant>-prod get cronjob <tenant>-backend-db-retention
```

`LAST SCHEDULE` shows when it last fired. The last three successful and three failed Job objects are kept:

```bash
kubectl -n <tenant>-prod get jobs -l app=<tenant>-backend-db-retention
kubectl -n <tenant>-prod logs job/<tenant>-backend-db-retention-<timestamp>
```

Every policy file reports before it deletes, so the log is a per-run audit trail: a line naming the file it's about to run, then a `table_name` / `eligible_for_deletion` count for every table-and-predicate combination in that file — the same count the delete then acts on. There's no separate report of exactly *which* rows were removed, only how many, per table, per run.
