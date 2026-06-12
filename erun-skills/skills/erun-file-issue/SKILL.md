---
name: erun-file-issue
description: Register or file a bug or feature request for the ERun project itself on GitHub. Use when the user says "file an erun bug", "file an erun feature", "register erun bug", "register erun feature", "open an erun issue", or any similar request about reporting an issue against the ERun platform (not the tenant repository being worked on).
---

# ERun issue and feature request

Use this skill when you (or the user) need to file a bug or feature request
against ERun itself — the platform that built and is running this remote
environment, or that the user is installing on their laptop — rather than
against the tenant repository being worked on.

## When to use

Trigger on user phrasings such as:

- "file an erun bug" / "file an erun feature"
- "register erun bug" / "register erun feature"
- "open an erun issue"
- any other phrasing that asks to report a bug or feature against ERun (the
  platform), not against the tenant repository.

## Repository

- Source: `https://github.com/sophium/erun`
- Issue tracker: `https://github.com/sophium/erun/issues`

The `gh` CLI is required. It is pre-wired inside a deployed ERun environment;
on a laptop the user must have it installed and authenticated against
`github.com` (`gh auth status` to check).

## File a bug

The environment block at the bottom of the body adapts to context. If the
caller is inside a deployed ERun env, `${ERUN_TENANT}` and `${ERUN_ENVIRONMENT}`
are set and `env | grep '^ERUN_'` returns useful data; on a laptop, none of
that is set and the env section should be omitted to avoid pasting empty
"unknown / unknown" lines into the issue body.

```sh
if [ -n "${ERUN_TENANT:-}" ]; then
    env_section=$(cat <<EOF

## Environment

- erun version: $(erun version 2>/dev/null | head -n 1)
- tenant / environment: ${ERUN_TENANT} / ${ERUN_ENVIRONMENT:-unknown}
- ERUN_* vars:
$(env | grep '^ERUN_' | sort | sed 's/^/  /')
EOF
)
else
    env_section=$(cat <<EOF

## Environment

- erun version: $(erun version 2>/dev/null | head -n 1 || echo "not installed")
- context: laptop (not inside a deployed env)
EOF
)
fi

gh issue create --repo sophium/erun --label bug \
    --title "<short, sentence-style title>" \
    --body "$(cat <<EOF
## What happened

<one or two sentences>

## What you expected

<one or two sentences>

## Reproduction

<commands or steps>
${env_section}
EOF
)"
```

## File a feature request

Same shape, but use `--label enhancement` and frame the body as a goal plus
acceptance criteria rather than a reproduction. The same `env_section` gating
applies.

## If you also intend to land a PR

- Branch from `main` in `sophium/erun`.
- Name the branch `feature/<issue-number>-<short-kebab-case>` or
  `bug/<issue-number>-<short-kebab-case>`.
- PR title: clean, sentence-style. Do not add agent markers like `[codex]` or
  `[claude]`.
- Include `Closes #<issue-number>` in the PR body when the PR is intended to
  close the issue.

If the user wants to do the work themselves, hand off; otherwise invoke the
`erun-contribute` skill to drive the full clone-to-PR flow.

## Important

Do not "fix" an ERun bug by editing a tenant-local copy of the runtime chart
or image. Environments deploy the published `erun-devops` chart and image
straight from the ERun release, so local edits never reach them. File the
issue and PR against `sophium/erun` instead.
