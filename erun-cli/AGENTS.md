# AGENTS.md

CLI transport guidance. Follow root `AGENTS.md` and the shared Go conventions
in `erun-common/AGENTS.md`.

## Module Role And Boundaries

- Own the `erun` binary and Cobra tree. CLI-private code belongs in `internal`;
  shared planning/execution belongs in common.
- The `mcp` command launches `emcp`; it must not embed its server.

## Command Wiring

- Constructors adapt arguments and bind flags. Move application logic into named
  `run...Command` helpers rather than substantial inline `RunE` closures.
- Register new capabilities in the real command tree and expose an operator
  surface where appropriate. Follow `erun-integration/AGENTS.md` §
  "Desktop-surface gate" for explicit agent-only/hidden/deprecated exceptions.

## CLI Help And MCP Tool Descriptions

This is the shared quality bar for CLI help and MCP descriptions.

- Establish ground truth from execution, including side effects, prompts, output,
  defaults, and errors; do not copy the sibling transport's wording unverified.
- Explain purpose and workflow position, not implementation inventories.
  `Short` is one line naming the operation and role. High-impact commands need
  a short `Long` naming data loss, cost, shared changes, or external sign-in.
- Reflect root primitive/version policy accurately. Convenience switches are
  operator shortcuts, not the default meaning of build/push/deploy/open.
- Every command must render usable `--help`, including forwarding commands using
  `DisableFlagParsing`; intercept help before forwarding.
- Include realistic `Example` invocations when flag combinations change behavior.
  CLI and MCP descriptions must agree semantically, while fitting their surfaces.
- Review literal rendered help for accuracy and usefulness, not length. Quote
  user-visible wording when reporting quality; cite source when identifying edits.
- The integration CLI-help gate checks registered example/prose flags and gate-run
  status vocabulary. It does not establish useful prose, truthful defaults, or
  complete descriptions; those still require review.

## Exit-Code Contract: Reporting Commands Vs Gating Checks

- `exec <verb>` is a gate: print the report and fail nonzero on findings.
  Never add an opt-out that makes a failed check green.
- `list` is a report: findings alone leave exit code zero. A drift report may
  offer explicit `--fail-on-drift`; execution errors still fail normally.
- Resolve ambiguous new commands by placement: gate under exec, report under list.
- MCP findings stay in structured results; only inability to run the check fails
  the tool call. Do not add an MCP equivalent of `--fail-on-drift`.

## Validation

- After Go changes run `go test ./...` here and relevant integration scenarios.
- CLI behavior is gated through the compiled binary, including help and each
  plan-changing flag combination. Remove unit tests duplicating those scenarios.
