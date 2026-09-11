# AGENTS.md

Follow root guidance. This module is the canonical source for ERun skills,
reusable agent definitions, and their Claude Code plugin manifest.

## Ownership and distribution

- Skills live at `skills/<name>/SKILL.md`, with supporting files beside them.
  Reusable agents are flat `agents/<name>.md` files. Do not duplicate bodies elsewhere.
- Runtime images copy these trees to `/etc/erun/skills` and `/etc/erun/agents`.
  The root marketplace distributes this module as the `erun-tools` git-subdir
  plugin. Its manifest uses automatic skill/agent discovery, not a hand-maintained list.
- Runtime installers populate both assistants' skills and Claude's reusable agents.
  Codex reusable-agent installation is not implemented here.
- Edit canonical sources, not baked or installed copies. A source change affects
  runtime fingerprints; no base-image fingerprint bump is needed for erun-devops.
  Marketplace publication updates its pinned source SHA as part of release, not
  before the source commit exists.
- Shared install helpers refresh absent, unchanged, and legacy unmarked copies,
  but preserve operator edits using baked-hash provenance. Skill directory markers
  and agent-file sidecar markers differ; keep both installers' tests aligned.

## Names and frontmatter

- Use `erun-<concern>` kebab-case, with matching directory/frontmatter/invocation
  names. Plugin namespace is applied at installation; do not bake it into names.
  Agent names must not collide with skill names.
- Require name and description. Describe purpose plus canonical trigger phrases
  for skills or delegation conditions for agents; selection needs specific intent.
  Use `disable-model-invocation` only for an intentionally user-invoke-only skill.

## Instruction design

- One workflow per user intent, not a chain of mechanical-step skills.
  Blueprint skills package best practices and blueprints; workflow skills drive
  a process. `erun-contribute` starts new issue/PR work, not an existing ticket.
- Keep bodies concise and context-aware. Detect pod context before using injected
  tenant/environment/output variables; check tools not reliably installed on hosts.
- Put non-git deliverables in the configured outputs directory when available;
  repository files still belong in git.
- Keep cross-cutting engineering/contribution/interaction rules in root guidance.
  Host lane selection, issue claims, supervision, and gate scheduling belong in
  erun-orchestrate and its provisioned instructions; merge-driving procedures belong
  in erun-merge-queue-drive. Direct in-pod Operators retain root's general rules.
- State the invariant, exception, and verification rather than incident narratives,
  redundant code inventories, or copied shell implementations. Preserve authorization
  boundaries and explicitly mark proposals and unverified capabilities.
- Keep runtime instruction provisioning separate from repository module guidance.
  Do not change its filename, symlink strategy, or refresh behavior incidentally.

## Validation and public docs

- Validate skill frontmatter/supporting references and both JSON manifests.
  Exercise changed instruction paths proportionally; for embedded instructions,
  test exact provisioned content and refresh behavior, not keyword presence.
- Distribution changes run `skills-install_test.sh` and `agents-install_test.sh`
  in the runtime image source. Image-layout changes verify every skill/agent ships;
  invocation changes verify actual discovery and selection in the relevant assistant.
  Do not claim a static validator proves live selection.
- For skill changes, update the existing built-in catalogue in
  `erun-docs/docs/agent-reference/skills-spec.md`: canonical name, verbatim
  description, triggers, inputs, outputs, and errors. Reusable-agent changes update
  `agents-spec.md`'s catalogue: role, observations, actions, and limits.
- Operator summaries in `docs/concepts/skills.md` stay one sentence per skill,
  linking to reference rather than duplicating schemas. Include public-contract
  changes in the same plan/PR. Pure compaction needs consistency checks; changed
  operational requirements need matching catalogue corrections.
