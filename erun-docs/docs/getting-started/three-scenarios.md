---
title: Three scenarios
---

# Three scenarios

Three things that used to mean tearing your dev stack down. With ERun, they all run side by side — your feature env never pauses.

<figure className="erun-hero-figure">
  <img src="/img/problem-vs-erun.svg" alt="Side-by-side comparison. Left half labelled 'Without ERun': three task pills (feature, peer review, hotfix) all funnel into a single 'Your dev stack' box; the feature arrow is active and cyan, the peer-review and hotfix arrows are dashed grey and labelled 'queued'. The stack shows 'in use: feature, queued: 2'. Right half labelled 'With ERun': the same three tasks each connect by an active cyan arrow into their own dedicated namespace (ns: feature, ns: peer-review, ns: hotfix), each marked 'full stack inside'." />
  <figcaption>One stack = one task at a time. A namespace per task = all in parallel.</figcaption>
</figure>

Each scenario below assumes you're already working in an env named `local`, with Operator and Agent collaborating on Feature A.

---

## 1 — A peer's pull request lands mid-feature

You're mid-feature. Your colleague pings you to review their PR. You need to actually run their branch — not just skim the diff — but your stack is hosting Feature A.

**The flow:**

```bash
# Add a worktree for their branch.
cd ~/code/myapp
git worktree add ~/code/myapp-review feature-from-peer

# Spin up a sibling env bound to that worktree.
erun init myapp review \
  --project-root ~/code/myapp-review \
  --kubernetes-context docker-desktop

erun open myapp review
```

Two envs run in parallel:

- `local` — Feature A, Operator + Agent untouched.
- `review` — peer's branch, full stack, ready to exercise.

When you're done:

```bash
erun delete myapp review
git worktree remove ~/code/myapp-review
```

Feature A never paused.

---

## 2 — Emergency hotfix while building a feature

Production is bleeding. You're mid-feature. Switching means tearing down — except it doesn't anymore.

**The flow:**

```bash
# Worktree for the hotfix branch.
git worktree add ~/code/myapp-hotfix hotfix/urgent

# Local-agent env on the prod cluster (see Hotfix pattern).
erun init myapp prod-local \
  --project-root ~/code/myapp-hotfix \
  --kubernetes-context erun-prod

erun open myapp prod-local
```

Inside `prod-local`, fix the bug, build, deploy to prod:

```bash
erun build --release
erun deploy <component> --tenant myapp --environment prod --version <new>
```

`local` (Feature A) never paused. Two envs share one cluster — `prod-local` develops, `prod` serves. Keep `prod-local` around as your active surface against prod, or `erun delete myapp prod-local` when you're done.

See [Hotfix pattern](/concepts/environment-types#hotfix-pattern) for the conceptual diagram.

---

## 3 — Waiting on CI integration tests

You finished Feature A. Integration tests need the full stack — and the build server queue is six PRs deep.

**The flow:**

```bash
# Run tests inside the env itself, against its own stack.
erun open myapp local
# In the pod's shell:
./test/integration.sh
```

The tests hit the env's own database, queue, and services. Green? Ship. Red? Fix in the same env, run again. No build-server queue.

**Multiple PRs at once?** Spin up one env per PR:

```bash
erun init myapp test-pr-101 --project-root ~/code/myapp-pr-101
erun init myapp test-pr-102 --project-root ~/code/myapp-pr-102
erun init myapp test-pr-103 --project-root ~/code/myapp-pr-103
```

Each env runs its own integration tests in parallel. The quality gate stops being a queue.

---

## What just happened

Three problems that used to serialize on one machine now run side by side. Each env is its own Kubernetes namespace, its own full stack, its own Operator + Agent collaboration — driven from the desktop app, the terminal, or an Agent over MCP. Same workflow for all three.

### See also

- **[Environment types](/concepts/environment-types)** — local-agent, remote-agent, runtime
- **[Inside an environment](/concepts/runtime-pods)** — what's in each namespace
- **[Operator in the loop](/collaboration/operator-in-the-loop)** — control surfaces over every env
