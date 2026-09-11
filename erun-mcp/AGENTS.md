# AGENTS.md

MCP transport guidance. Follow root `AGENTS.md` and shared Go conventions in
`erun-common/AGENTS.md`.

## Module Role And Boundaries

- Own `emcp`, HTTP/SDK wiring, configuration, and tool registration here.
  Handlers adapt explicit inputs to shared plans and return structured results.
- Follow root primitive/version and preview contracts; never synthesize a missing
  push/deploy version or rely on interactive prompts.
- Findings are structured results, not tool-call failures. Fail calls when the
  check could not run; see CLI "Exit-Code Contract: Reporting Commands Vs Gating Checks".

## The WebSocket Attach Edge Is Not An MCP Tool

- `attach.go` serves `GET <mcpPath>/attach/{session}` beside JSON-RPC. A live PTY
  needs a duplex stream, not an MCP tool result. Run the attach subprocess locally
  in the pod; do not introduce a platform gateway with `pods/exec`.
- Authenticate and check `erun:attach` before upgrade or subprocess creation.
  Browser auth uses exactly `[attachAuthSubprotocol, token]` in subprotocols;
  header auth remains supported. Echo only the scheme, never the token.
  This fallback is attach-only; JSON-RPC still requires its bearer header.
- Preserve `http.Hijacker`: do not wrap attach in the ordinary byte-counting
  traffic middleware. Record activity directly.
- Binary frames carry PTY bytes; text frames carry resize control and one terminal
  outcome before close. Unknown/killed/unreaped outcomes must not become guessed
  ended/taken-over outcomes.
- Allocate a controlling PTY. Killing the attach process group detaches only that
  client, not the session master. Create the session directory with mode 0700
  before launch; a first console attach cannot depend on prior CLI initialization.
- Keep the real dtach/PTY fresh-directory regression test in `attach_test.go`.
  The browser caller is `erun-console/src/mcp/attachClient.ts`; the protocol
  reference is `erun-docs/docs/agent-reference/api-protocol.md`.

## A Browser Caller Needs Two Cross-Origin Fixes, Not One

- Response CORS headers do not disable the SDK's request-side
  `CrossOriginProtection`. Preserve both the reflected-origin response and the
  path-scoped bypass in `crossOriginProtectionForAuthenticatedEdge`.
- The bypass is justified only by mandatory, explicit bearer authentication and
  absence of ambient cookie credentials. Revisit it if auth changes to cookies;
  do not widen it to unrelated paths.
- Validate with a real cross-origin browser request, not only a non-browser client.

## Desktop Restart Is A CLI Verb, Not An MCP Tool

Desktop restart targets host loopback. Even local-agent MCP runs in a pod with a
different network namespace. Keep restart host-side; do not add an unreachable tool.

## Host AWS Credentials Are A CLI Verb, Not An MCP Tool

- Host SSO refresh belongs to `erun cloud refresh`; the pod cannot originate it.
- Preserve the existing inject/clear tools used by desktop delivery, including
  their warning against argument-recording callers and pointer to the CLI.
- Do not add credential-bearing tool arguments. Deliver new secrets through
  stdin behind constant scripts or mounted Secrets, not recorded argument lists.

## MCP Tool Descriptions

Apply `erun-cli/AGENTS.md` § "CLI Help And MCP Tool Descriptions", including
parameter descriptions. Both transports must describe the same actual behavior.

## Diagnosing A Deployed Runtime Via MCP

- Inspect state from its owning machine: pod evidence does not establish host
  configuration. Prefer structured tools; use raw argv only for missing evidence.
- A desktop-open environment's MCP port is in
  `<UserConfigDir>/erun/portforward/mcp/<tenant>/<environment>.json`.
  Direct JSON-RPC diagnostics require initialize → initialized notification →
  calls with the returned `Mcp-Session-Id` and JSON/SSE acceptance.
- Confirm the running image, source commit, required toolchain, process, and bound
  port before repeating a slow user-facing flow. Missing prerequisites and stale
  code require different remedies; a successful probe is not end-to-end proof.
- These diagnostics remain available to direct in-pod/operator sessions without
  an orchestrator. Host orchestration procedures belong in `erun-orchestrate`;
  avoid copying ad hoc curl recipes or unconditional checkout commands here.

### Verifying in-pod fixes before re-running the user-visible flow

Verify the changed artifact is actually running before attributing a repeated
failure to it. If prerequisites and version are correct, investigate the failing
boundary (desktop wiring, port-forward, browser) instead of rebuilding blindly.

## Validation

- After Go changes run `go test -count=1 ./...`; doc/source readers must not reuse
  cached results. Attach/auth changes also exercise the corresponding console
  browser tests under their own guide.
