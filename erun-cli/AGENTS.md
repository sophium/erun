# AGENTS.md

Module-specific guidance for `erun-cli`. Follow the repository root `AGENTS.md` first, then apply this file.

`erun-cli` is the CLI transport — the `erun` binary and its Cobra command tree. It follows the shared Go conventions in `erun-common/AGENTS.md` (code organization, dependency wiring, visibility, naming, Go safety) and adds the CLI-specific rules below.

## Module Role And Boundaries

- Keep CLI-private implementation in `erun-cli/internal`.
- Treat `internal` as a deliberate module boundary, not a staging area for future shared code.
- `erun-cli` may depend on `erun-common`, but its `mcp` command is only a launcher for the `emcp` executable and must not embed MCP server logic.
- `erun-cli` and `erun-mcp` must not import each other.
- `erun-cli` should reach backend functionality through transport-neutral clients and contracts in `erun-common`, not by importing backend API packages directly.
- By default, new commands should be implemented in both transports: CLI and MCP. Treat a command as shared work unless there is a clear repository-specific reason for it to exist in only one transport. Keep the CLI layer thin; shared planning and execution belong in `erun-common`.

## Command Wiring

- In CLI command constructors, keep inline `RunE` closures thin. Use them for Cobra argument adaptation and flag binding, but move real command/application logic into named package functions.
- If a command already has meaningful application logic such as resolving shared results and rendering them, prefer a named `run...Command` or equivalent helper over leaving that logic inline in the Cobra definition.
- A new command must be reachable from the desktop app, not just this transport and MCP — see root `AGENTS.md` § "Smooth, Seamless, No Dead Ends". `erun-integration`'s desktop-surface gate (`erun-integration/AGENTS.md` § "Desktop-surface gate") enforces this: any CLI command with no matching MCP tool must have a real reference in `erun-ui/frontend/src`, unless it is Cobra `Hidden`/`Deprecated` (an internal-lifecycle command already marks itself that way) or explicitly declared exempt in `cliOnlyAgentFacingCommands` (`erun-cli/cmd/command_tree.go`) because it is genuinely agent- or tooling-only.

## CLI Help And MCP Tool Descriptions

`erun --help` (every Cobra command's `Short:` / `Long:` / `Example:` and every flag usage string) and the MCP server's tool `Description:` + parameter `jsonschema:"…"` strings are the only place a reader — human or LLM — can learn how to use a command and what it will do before running it. Treat them as part of the product's public surface, not as cosmetic strings. (This section is the shared quality bar for both transports; `erun-mcp/AGENTS.md` points here.)

Quality bar:

- Help is useful, not exhaustive. A reader should come away knowing what the command is for, when to reach for it, and — where it matters — where it sits in the larger workflow. Conveying that context is the job; enumerating internals is not. Detail that does not change how someone uses the command (every file written, every intermediate git/helm/kubectl call) is noise — leave it out.
- Orient the reader in the flow when a command is part of one. `build`, `push`, `deploy`, and `open` are pure primitives (build → push → deploy → open); each `Short:`/`Long:` should describe only that command's one job. Good help also says what a command's *convenience* switches fold in (`build --deploy` pushes the result and rolls it out; `open --deploy` deploys before opening) — but frame those as operator shortcuts, not the command's core behaviour, and never imply `deploy` builds or `push` decides based on env. The version-required rule is part of the contract: `push`/`deploy` install/publish an explicit version (`deploy` also takes `--current` for the env's running version); say so where it matters. See root `AGENTS.md` § "Command primitives vs orchestration".
- Every command must be help-discoverable. `erun <cmd> --help` must print usable help even for subcommands that need `DisableFlagParsing`; intercept `--help` before forwarding the rest to the underlying command. A command whose `--help` does not render is a bug.
- A leaf command's `Short:` names the operation and the role it plays, not the topic, and stays to one line. "Delete an environment" is the topic; "Delete a tenant environment and tear down its remote runtime" is the operation. Keep consequences out of the `Short:`.
- High-blast-radius commands — anything that mutates remote k8s, AWS resources, the project's git state, or destructive local state (`init`, `deploy`, `delete`, `open`, `release`, `doctor`, `context init`, `cloud init aws`, `build --release --deploy`, and equivalents) — carry a short `Long:` that states only the consequences a reader must weigh before running: irreversible data loss, money spent, remote or shared state changed, a browser/SSO login that will pop. State them plainly in a line or two; do not turn the `Long:` into an exhaustive side-effect log.
- Every command whose flag combinations meaningfully change behaviour needs an `Example:` block with at least one realistic invocation.
- The CLI `Short:` + `Long:` and the MCP tool `Description:` for the same operation must reflect the same ground-truth behaviour. If they diverge in meaning, one of them is wrong — fix both, not just one.

**Two narrow slices of this are mechanically checked, not just reviewed:** `erun-integration/cli_help_flag_drift_test.go` (see `erun-integration/AGENTS.md` § "CLI help drift gate") cross-checks every literal `erun ...` invocation in a command's own `Example:` field, and every backtick-quoted invocation in any command's `Short:`/`Long:`, against the real command tree — a flag mentioned in help or reached for in an example that was never registered fails there — plus the gate-run status vocabulary specifically, against erun-backend-api's real accepted values and case-normalization. That gate does not read for usefulness, tone, or whether a flag's prose description (what it does, its documented default) still matches reality; the quality bar and methodology above stay a human review job for everything outside those two checks.

Methodology when reviewing or editing help / descriptions:

- Establish ground truth first. Trace the command's execution path end-to-end and write down what it actually does — operations performed, side effects, files written, network calls, prompts, output, error paths. Only then read the existing `Short:` / `Long:` / MCP `Description:` and score each against that ground truth. Reviewing docs against impressions of the code, or against the sibling transport's docs, is not the review — it produces false positives.
- Score descriptions by accuracy, not length, and by usefulness, not completeness. A longer description that misframes the operation is worse than a short one that gets the noun right. "Run Docker build operations" reduces an orchestrator to a docker proxy; replacing the CLI's `Short:` with that wording makes the help worse, not better. Judge whether a reader learns what the command is for and when to use it — not whether every behaviour is listed.
- Do not blanket-lift one transport's description into the other. Each must be rewritten against the ground truth.
- When reporting on help-text quality, quote the literal user-visible text (the `--help` block, the MCP description) and reason about what a reader can infer from it. Skip file:line citations — they do not change whether the help works. Reserve source references for places where something must be changed.

## Exit-Code Contract: Reporting Commands Vs Gating Checks

A command's default exit-code behavior follows its command family, not an ad hoc choice per command. Two shapes exist today, and a new command must pick the one that matches what it is, never invent a third:

- **`erun exec <verb>` commands are gating checks.** Their whole purpose is to be wired into automation as a pass/fail gate (a merge queue, a cron job, a CI script). They print the full report, then exit non-zero by default whenever what they checked is not fully green — `route-check` (missing or unreachable routes), `gate-merge`, `reconcile-bypass` all follow this shape. Never add a flag that makes an `exec` command exit 0 on a real finding; a caller who wants to ignore a finding can do that with its own logic, but the check itself must keep failing loudly.
- **`erun list` (and any future descriptive/enumeration command) reports.** Its job is to tell a human or a script what state exists, not to pass judgment on whether that state is acceptable, so its default exit code stays `0` regardless of what it finds — this is what keeps a plain `erun list` safe to pipe into `grep`/`jq` without ever needing to check `$?`. When a `list` variant surfaces something worth gating on (deployed-vs-published control-plane drift, environment-vs-gate version drift), it grows an explicit opt-in flag, `--fail-on-drift`, that a caller passes only when that specific invocation should fail non-zero on the condition the report already flags. Without the flag, behavior is unchanged.
- The two shapes must never blur into each other. An `exec` command must never gain an opt-out that suppresses its default failure; a `list` variant must never fail by default without `--fail-on-drift` being passed. If a new command's purpose sits ambiguously between "report" and "gate", that ambiguity is a naming/placement problem to resolve — `exec` for a gate, `list ...` for a report — not a reason to pick a shape and hope the exit code follows.
- This is a CLI-only contract. MCP tools never fail a call over a finding, only over the check itself failing to run, regardless of which exit-code shape the sibling CLI command follows — an MCP caller (an agent) reads the returned structured result and judges it itself, so `--fail-on-drift` has no MCP-side equivalent and none should be added for it.
- The cost of skipping this: three related drift checks (`erun list --tenant`, `erun list --control-planes`, `erun exec route-check`) shipped with two different exit behaviors and no stated rule connecting them, so an automation author had no way to predict which of erun's own checks could be wired into a gate at all (erun#2052).

## Validation

- Run `go test ./...` from this module after Go changes.
- `erun-cli` behavior is gated end-to-end by the integration suite, not by unit tests (root `AGENTS.md` § "Integration Test Gate"; `erun-integration/AGENTS.md`). Add or update integration scenarios for new CLI behavior — including `--help` and every flag combination that changes the resolved plan — and delete unit tests that overlap an integration scenario.
