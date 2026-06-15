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

## CLI Help And MCP Tool Descriptions

`erun --help` (every Cobra command's `Short:` / `Long:` / `Example:` and every flag usage string) and the MCP server's tool `Description:` + parameter `jsonschema:"…"` strings are the only place a reader — human or LLM — can learn how to use a command and what it will do before running it. Treat them as part of the product's public surface, not as cosmetic strings. (This section is the shared quality bar for both transports; `erun-mcp/AGENTS.md` points here.)

Quality bar:

- Help is useful, not exhaustive. A reader should come away knowing what the command is for, when to reach for it, and — where it matters — where it sits in the larger workflow. Conveying that context is the job; enumerating internals is not. Detail that does not change how someone uses the command (every file written, every intermediate git/helm/kubectl call) is noise — leave it out.
- Orient the reader in the flow when a command is part of one. erun's release/deploy commands form a pipeline — build → release → push → deploy. Good help says where a command sits in that flow and what its convenience flags fold in (e.g. `build --release` resolves and stamps the release version before building; `build --deploy` pushes the result and rolls it out — so one command runs steps you would otherwise run by hand), not the git tags or helm upgrades underneath.
- Every command must be help-discoverable. `erun <cmd> --help` must print usable help even for subcommands that need `DisableFlagParsing`; intercept `--help` before forwarding the rest to the underlying command. A command whose `--help` does not render is a bug.
- A leaf command's `Short:` names the operation and the role it plays, not the topic, and stays to one line. "Delete an environment" is the topic; "Delete a tenant environment and tear down its remote runtime" is the operation. Keep consequences out of the `Short:`.
- High-blast-radius commands — anything that mutates remote k8s, AWS resources, the project's git state, or destructive local state (`init`, `deploy`, `delete`, `open`, `release`, `doctor`, `context init`, `cloud init aws`, `build --release --deploy`, and equivalents) — carry a short `Long:` that states only the consequences a reader must weigh before running: irreversible data loss, money spent, remote or shared state changed, a browser/SSO login that will pop. State them plainly in a line or two; do not turn the `Long:` into an exhaustive side-effect log.
- Every command whose flag combinations meaningfully change behaviour needs an `Example:` block with at least one realistic invocation.
- The CLI `Short:` + `Long:` and the MCP tool `Description:` for the same operation must reflect the same ground-truth behaviour. If they diverge in meaning, one of them is wrong — fix both, not just one.

Methodology when reviewing or editing help / descriptions:

- Establish ground truth first. Trace the command's execution path end-to-end and write down what it actually does — operations performed, side effects, files written, network calls, prompts, output, error paths. Only then read the existing `Short:` / `Long:` / MCP `Description:` and score each against that ground truth. Reviewing docs against impressions of the code, or against the sibling transport's docs, is not the review — it produces false positives.
- Score descriptions by accuracy, not length, and by usefulness, not completeness. A longer description that misframes the operation is worse than a short one that gets the noun right. "Run Docker build operations" reduces an orchestrator to a docker proxy; replacing the CLI's `Short:` with that wording makes the help worse, not better. Judge whether a reader learns what the command is for and when to use it — not whether every behaviour is listed.
- Do not blanket-lift one transport's description into the other. Each must be rewritten against the ground truth.
- When reporting on help-text quality, quote the literal user-visible text (the `--help` block, the MCP description) and reason about what a reader can infer from it. Skip file:line citations — they do not change whether the help works. Reserve source references for places where something must be changed.

## Validation

- Run `go test ./...` from this module after Go changes.
- `erun-cli` behavior is gated end-to-end by the integration suite, not by unit tests (root `AGENTS.md` § "Integration Test Gate"; `erun-integration/AGENTS.md`). Add or update integration scenarios for new CLI behavior — including `--help` and every flag combination that changes the resolved plan — and delete unit tests that overlap an integration scenario.
