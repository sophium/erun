---
title: Reviews
---

# Reviews

The tenant dashboard is where the desktop surfaces your hosted tenant: its reviews, and — on a separate tab — the tenant's own registration state.

### Reviews tab

The tenant dashboard's **Reviews** tab lists every review for the signed-in tenant — status, source and target branch — with a color badge that always carries a text label alongside it. Open a row for that review's own detail: its recorded builds, its position in its target branch's merge queue, and its comment threads. Reply to an existing thread from there; the reply is attributed to you and, if the submit fails, your draft text stays put so you don't lose it. **New review** opens one without leaving the app: it names the environment it acts in, commits and pushes that environment's current branch as two steps you trigger yourself (skip the commit when the branch is already committed), then proposes it into a target branch offered from the ones your reviews already use. The same dialog also opens directly from an environment's diff panel — its own **Start a review** button prefills the environment and the target branch from what the diff panel is already showing, so nothing visible there needs retyping; push still stays a step you trigger, and a failed push names the reason and leaves the button ready to retry. If your account may not create reviews for that tenant, the dialog says so instead of only failing once you submit.

### Merge queue and comment threads

A review's own detail adds **Close review**, and the **Merge queue** tab an **Advance queue** action — each behind a confirm step, and each replaced by the access you are missing when your account may not use it. If the queue head still has unresolved comment threads, advancing is refused and names the count and the review, with **View discussion** to open it directly; an account with the separate override permission can instead bypass the check by stating a reason, which is recorded against your identity. Only a thread's own root-comment author can resolve or reopen it — this dashboard included — so if that refusal names a thread you didn't open, ask its author or use the override rather than trying to close it yourself. To start a *new* comment thread rather than reply to one, hover a line in the review panel's diff and use the comment affordance that appears; the thread is anchored to that file and line. See [`erun review`](/cli/review) for the same actions from the CLI, and [Merge queue](/collaboration/merge-queue) for the full mechanics — including recovering a merge whose gate build gets stuck, which has no button here yet.

### Keyboard shortcuts in the review surface

The diff panel and a review's comment threads are fully keyboard-operable, not just mouse-driven. In the diff panel: **↓ / ↑** move to the next or previous hunk (crossing into the next file once you pass the last one), **] / [** jump straight to the next or previous changed file, and **S** starts a review for the environment section you're focused in. In a review's comment threads: **↓ / ↑** move between threads, **R** replies to the focused one, and **Enter** resolves or reopens it. A **Keyboard shortcuts** button — beside **Refresh diff** in the changed-files panel, and in the review detail dialog's own header — lists the same bindings without you needing to leave the keyboard to find them.

### Getting the tenant dashboard connected {#tenant-dashboard-connected}

Opening a tenant's dashboard resolves the same platform identity [`erun platform`](/cli/platform) uses, and names exactly what is missing rather than one generic sign-in error: **not connected** offers a **Connect** field (prefilled with the tenant's likely address) that attaches the platform and signs you in in one step; **not signed in** offers **Log in** for the same device-code/browser flow `erun cloud login` runs; **not enrolled** — your sign-in succeeded but this tenant doesn't recognize you yet — shows the exact `erun platform user enroll` command an administrator needs, with a one-click copy, and a **Try to enroll myself** action for the rare case your account already has that access; and a plain permission refusal just names who to ask. Signing in successfully re-fetches the dashboard on its own — no Refresh click needed. The same alias also has its own home in **Settings → Cloud aliases**, alongside your AWS and Cloudflare aliases — an **Add erun platform** action there asks for the platform's API URL and connects and signs in the same way, so you can attach a hosted platform without opening a tenant dashboard first.

### Registration tab

The tenant dashboard's **Registration** tab is where a tenant/environment you created in the desktop gets its hosted counterpart — the local sidebar and the platform's own tenant/environment rows are separate objects, and this tab names that plainly and lists what is already registered on the platform. Cloud contexts: **Preview context plan** resolves the bootstrap plan before **Register context** creates anything for real. Hosted environments: **Preview provisioning plan** resolves quota, placement, namespace, and deploy order for a drafted environment before **Register environment** commits to it; each registered row then carries its own **Deploy** (with an optional version field), **Stop**, and **Delete** — Delete asks you to type the environment's name to confirm, the same as every other unrecoverable action in the app. Hitting your tenant's environment-count cap shows the platform's own message inline rather than a raw error, naming the fix (delete or stop another environment first) instead of just failing. Registering a new tenant, or enrolling its first user, still needs `erun platform tenant create`/`erun platform user enroll` from a terminal or the console — the tab explains this and does not attempt to half-configure it through a form. See [Managing hosted environments](/collaboration/hosted-environments) for the same actions from the CLI.

## Where next

- [Merge queue](/collaboration/merge-queue) — why the queue exists, what the gate does, and recovering a wedged gate.
- [Activities and recovery](/desktop/activities-and-recovery) — the operations queue, failed-deploy recovery, and closing the window while work runs.
- [`erun review`](/cli/review) — the same review actions from the CLI.
- [Managing hosted environments](/collaboration/hosted-environments) — the Registration tab's actions from the CLI.
