---
title: Deploying a version
---

# Deploying a version

An environment's settings → **Runtime** tab is where you point an environment at a published version, choose the chart it installs, and keep a fleet of environments current.

### Deploy a version from the Runtime tab

The **Version to deploy** picker. Choose a published version and **Deploy** installs exactly that version by reference — it never rebuilds — so the button stays disabled until you pick one. The same picker lists the component charts to roll out (and you can save them as this environment's default), but only once you've picked a version — the panel is sequential: pick a version, then choose which charts deploy. The charts offered are the ones published at that version — the same for every environment, because the version decides which charts exist. When the environment builds from your local source, a separate **Create & deploy new version** action builds a fresh version, publishes it, and deploys it in one step. See [command primitives](/concepts/command-primitives).

### State which chart the runtime rides

Under the version picker, **Runtime chart** names the second half of what a deploy installs. A version normally names both artifacts — the chart and the runtime image — because [`erun push`](/cli/push) publishes them together. When a project versions its runtime image on its own release line, it does not: the chart is ERun's and exists only at ERun's versions. The field offers the ERun chart versions available to this environment (every offered entry is a chart that exists), writes the full reference it picked, and remembers it, so later deploys — including from this tab — install that chart while the version keeps naming the image. Left as **Published with the deployed version**, nothing changes. See [`erun deploy`](/cli/deploy#runtime-chart-coordinate).

- **A version with no chart says so before you deploy it.** Picking a version resolves which chart it would install and reports it: a version on your project's own release line has no chart at all, so the panel names that where you are choosing, **Deploy is disabled** rather than starting a rollout that cannot succeed, and a one-click **Use ERun ‹version›** sets the chart that fixes it. When the environment already states a chart, the same line reads back what will be installed (`Runtime chart erun-devops 1.0.178, set on this environment`). A registry ERun cannot list (private or unreachable) is never reported as "no chart" — it says nothing and leaves the deploy to you.

### Upgrade all

The **Upgrade all** button in the Environments header redeploys every environment opted into the upgrade set to the latest version for its channel (stable or snapshot; a snapshot-channel environment adopts a stable release once one is published on top of the latest snapshot). It opens a preview dialog listing each member and its current → target version, and only upgrades the ones that lag — opt an environment in and pick its channel from the environment's Runtime settings. Confirming runs each member's upgrade **in its own environment, in parallel**: progress and any failure show up on that environment's Local tab, sidebar row, and Activities entry, not in someone else's terminal. A member whose latest version can't be determined (for example the registry refused the lookup) shows **latest unknown** with the reason and is left untouched rather than guessed at. When an environment's [listed registries](/deployment/registries) offer more than one newer version, the row shows a **picker** — choose the version (each labelled with the registry it came from) and that member joins the upgrade. See [`erun upgrade`](/cli/upgrade).

## Where next

- [Resources and usage](/desktop/resources-and-usage) — an environment's own CPU, memory, and disk readings after you deploy.
- [Activities and recovery](/desktop/activities-and-recovery) — what happens when a deploy fails, and how to recover it.
- [`erun deploy`](/cli/deploy) — the same action from the CLI.
