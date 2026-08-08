---
name: erun-pin-version
description: Change or try the erun version an environment uses, by re-pinning every place that version is recorded — the Terraform module refs, each umbrella chart's erun dependencies, the build-env image tag, and the environment's own runtime version — in one verified, idempotent motion, then reverting just as easily if it doesn't work out. Use when the user says "change the erun version", "try a newer erun", "pin erun to <version>", "upgrade this environment to erun <version>", "what erun versions can I pin to", "the terraform ref and the charts disagree", "realign the erun pins", "revert the erun version", "roll back the pin", or any similar request to move or align an environment's erun version.
---

# Change an environment's erun version

One erun version is written down in several places, and they only work when
they agree. Nothing else keeps them in step, so they drift — a repo was found
with Terraform on `1.0.102`, its charts on `1.0.106`, and the running binary on
`1.0.115`, and realigning it by hand meant editing seven files.

`erun pin` moves all of them together. This skill is the agent-facing wrapper
around it: resolve a target, verify it is real, show the plan, apply, report.

## What counts as a pin site

| Site | Where |
|---|---|
| Terraform module ref | `?ref=v<version>` on every `github.com/sophium/erun.git//…` source |
| Helm chart dependencies | each `<tenant>-<component>` umbrella's `Chart.yaml` erun dependency `version:` |
| Build-env image | `FROM …/erun-devops:<version>` in a custom runtime image |
| Environment runtime version | the env's `runtimeversion` |

Only erun's own references. A tenant's own Terraform sources, their own chart
dependencies, and an umbrella's own `version:` are versioned independently and
are never touched.

## The motion

1. **Discover** what can be pinned to:

   ```bash
   erun pin --list
   ```

   Offer the operator real published versions rather than asking them to recall
   one. `latest stable` is the default target.

2. **Show the plan before writing.** Always dry-run first, and show it:

   ```bash
   erun pin <tenant> <env> --version <X> --dry-run
   ```

   Every site, its current value and its new one. If the plan reports no
   changes, the tree is already aligned — say so and stop; re-running is a
   no-op by design, not a reason to force anything.

3. **Apply**:

   ```bash
   erun pin <tenant> <env> --version <X>
   ```

   This rewrites the pins, records the version being left, and regenerates the
   `Chart.lock` of every chart it moved.

4. **Report** what changed — the count and the old → new — and state plainly
   that nothing is deployed yet.

5. **Realize it, as a separate explicit step**: `erun terraform apply`, then
   `erun deploy`. Never fold these into the re-pin: changing a pin must not be
   a rollout by accident.

## Reverting

```bash
erun pin <tenant> <env> --revert
```

Goes back to the version recorded before the last re-pin. Re-pinning to a
version the tree already holds does not overwrite that record, so a revert
still reaches the version you actually came from.

## Rules

- **Never pin to an unpublished version, and never to `main`.** The command
  verifies against the registry and refuses; do not work around the refusal.
  A tree pinned to something unpublished fails much later, at a `terraform
  init` or a chart pull, far from the cause.
- **A registry that cannot be read is "could not verify", not "not
  published".** With an explicit `--version` the command pins anyway and says
  it could not check. Resolving *the latest* genuinely needs the registry and
  fails without it — do not substitute a guess.
- **Show the plan before applying**, every time, even when the operator asked
  for a specific version. A re-pin edits files across a repo.
- **Idempotent.** Safe to re-run. An aligned tree reports no changes.
- **Do not hand-edit the pin sites.** If a site is not being picked up, that is
  a gap in the command worth reporting (`erun-file-issue`), not something to
  patch around by editing the file yourself — a hand edit is exactly the drift
  this exists to end.

## Error behaviour

| Failure | What to do |
|---|---|
| Target not published | Report it and offer `erun pin --list`; do not force it. |
| `--revert` with nothing recorded | Say there is no recorded previous pin. Ask for an explicit version instead. |
| Already aligned | Report the no-op. Nothing to do. |
| `helm dependency update` fails on a chart | The command names the chart and stops. Fix that chart's access to the registry, then re-run — the re-pin is idempotent. |
| Project root not resolved | Run from inside the tenant repository. |

## Important

- The re-pin edits the **source of truth**. It does not build, push, or deploy,
  and it never mints a version.
- It is complementary to `erun upgrade`, which redeploys the running runtime
  for channel-opted-in environments. Use `pin` to change what the repo says;
  use `upgrade` to move a running environment along its channel.
- The same operation is available as the `pin` MCP tool for agents driving an
  environment over MCP, with `preview` for the plan.
