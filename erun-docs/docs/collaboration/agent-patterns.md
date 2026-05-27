---
title: Agent patterns
---

# Agent patterns

Common patterns for Agent authors. None are required — ERun's primitives are flexible enough that an Agent can implement any of these on top of the [MCP tools](/mcp/overview) and the [erun API](/collaboration/overview). What follows is the set most teams converge on.

## 0. Skill before guessing

If ERun ships a [skill](/concepts/skills) for what the Operator is asking, load it before writing code. Don't guess at the convention; let the skill teach you the layout, the Dockerfile pattern, the chart structure, the deploy-plan rule.

```
// Operator says: "Add a Go service called 'api'."
// Wrong: write the Dockerfile, chart, source by hand from your training data.
// Right:
//   1. Discover the matching skill — `ls ~/.claude/skills/` (or your loader's equivalent).
//   2. Read SKILL.md for `go-service`.
//   3. Write the source + Dockerfile + chart by hand, conformant to the skill's guidance.
//   4. Append the component to the deploy plan in .erun/config.yaml.
//   5. Show the Operator the diff.
```

A skill is guidance, not a template. You can adapt the shape to fit the Operator's description — gRPC vs. HTTP, library choice, dependency pinning — while still honouring the project's conventions. See [Skills spec](/agent-reference/skills-spec) for the bundled set.

## 1. Orient first

Before any action, get the lay of the land.

```jsonc
// MCP
tools/call name="list"      // tenants, envs, effective target
tools/call name="version"   // CLI + runtime versions
```

`list` and `version` are free in audit-cost terms — they're read-only and structured. An Agent that orients before acting is much easier to follow in the audit trail.

## 2. `doctor` before `raw`

If `raw` returns an unexpected result, run `doctor` next. `raw` is the escape hatch; `doctor` tells you whether the env itself is in a known-bad state.

```jsonc
// MCP
tools/call name="raw" arguments={ "argv": ["./scripts/seed.sh"] }
// → exit_code: 1, stderr: "fatal: not a git repository"

tools/call name="doctor"
// → checks.git_checkout = "fail: missing"

// Now you know: re-init the env (the marker is gone), don't retry raw blindly.
```

The audit-trail rule: every failed `raw` should produce a follow-up `doctor` call. An Operator reviewing later sees the Agent diagnosing, not just retrying.

## 3. `idle` before sleeping

If the Agent is about to wait on user input or a long-running build, check whether the env is going to idle-stop under it.

```jsonc
tools/call name="idle"
// → eligible_for_stop: true, last_terminal_input: <2h ago>

// Either pump activity (touch a file, run a `list`), or warn the user the env may stop.
```

This makes the Agent self-aware about the environment's lifecycle.

## 4. Build → verify → deploy

The three-step ship pattern, end-to-end inside one env:

```bash
# In the agent env:
erun build --dry-run     # 1. preview — Operator can review
erun build               # 2. actually build
./test/integration.sh    # 3. verify against the env's own stack
erun deploy --dry-run    # 4. preview deploy
erun deploy              # 5. roll out
```

Each step lands in the audit trail. The dry-run pairs make the plan auditable before any side effect.

## 5. Review → comment → fix loop

The Agent's natural fit for peer review is a tight loop:

```
# 1. Open a review (POST /v1/reviews)
# 2. Post inline comments on specific commit + line (POST /v1/reviews/{id}/comments)
# 3. The other Agent (or Operator) replies with counter-comments
# 4. The original Agent reads them (GET .../comments) and pushes fixes
# 5. Build (POST .../builds) + status PATCH lands the review in READY
# 6. Merge queue advances (POST /v1/reviews/merge-queue/advance)
```

Avoid spamming — rate-limit your own comments to one per discussion thread until the other side replies. The API itself rate-limits you anyway (see [Rate limits](/agent-reference/api-protocol#rate-limits)).

## 6. Self-healing via `doctor`

When the Agent observes a known bad state, prefer `doctor` recovery over scripted fixes:

```jsonc
// MCP
tools/call name="raw" arguments={ "argv": ["git","status"] }
// → exit_code: 128, stderr: "fatal: not a git repository"

tools/call name="raw" arguments={ "argv": ["erun","doctor","-y"] }
// → checks.* now ok, env restored

// Resume the original task.
```

`doctor`'s recovery actions are known to the platform; ad-hoc fixes from the Agent aren't. Lean on `doctor`.

## 7. Spawn a sibling for parallelism

When a task naturally parallelises (e.g., regenerate three flavours of generated code; run integration tests across N feature branches), an Agent can spin up a sibling env:

```jsonc
tools/call name="raw" arguments={
  "argv": ["erun","init","my-tenant","claude-review-2","-y"]
}
// → exit_code: 0
// → the new env appears in `list` shortly after
```

The Agent doing the spawning is responsible for tearing it down:

```jsonc
tools/call name="raw" arguments={
  "argv": ["erun","delete","my-tenant","claude-review-2","-y"]
}
```

Keep spawn / delete in matched pairs. An Operator reviewing the audit trail should see balanced bookends.

## 8. Append-only history

Builds, comments, review-status transitions, audit events — all append-only. The Agent never edits old records; it appends a corrective one. A new build supersedes the previous one; a "false alarm" comment is closed (status `CLOSED`), not deleted.

This keeps the audit story clean: every change is its own row with its own actor + timestamp.

## 9. Service-account identity

Agents running unattended use a service-account OIDC identity (see [Sign-in](/agent-reference/api-protocol#sign-in-oidc)). The Agent's `sub` claim resolves to a stable `creator_user_id` in every audit record — there is no anonymous Agent action.

Practical implications:

- Use a *different* service account per Agent. Distinct identities in the audit trail are how the Operator distinguishes Agent A's actions from Agent B's.
- Rotate credentials on a regular cadence; the service-account refresh flow re-issues short-lived tokens without code changes.

## 10. Be loud in `--dry-run`, terse in real runs

The dry-run trace is for the Operator; the real-run trace is for diagnostics. Match accordingly:

- `--dry-run`: maximally verbose, include reasoning ("would build because Dockerfile changed").
- real run: terse, factual ("docker build → success").

The audit log captures both; the Operator filters by `result: "dry_run"` to see the planning side.

## See also

- **[MCP overview](/mcp/overview)** — the underlying tools.
- **[Operator + Agent overview](/collaboration/overview)** — the cross-env coordination layer.
- **[Operator in the loop](/collaboration/operator-in-the-loop)** — what the Operator sees from the other side.
