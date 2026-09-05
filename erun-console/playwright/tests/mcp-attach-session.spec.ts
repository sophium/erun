import { expect, test } from '@playwright/test';

// Real end-to-end proof for erun#1692's browser-side attach edge: a console
// session drives AttachSessionForm (src/mcp/attachClient.ts +
// controller.ts's useAttachSessionController) against a REAL emcp instance's
// WebSocket attach edge -- real dtach, real PTY, real bytes -- from a real
// Chromium tab at a different origin than the edge. `MCPAccessPanel.test.tsx`
// and `attachClient.test.ts` only ever exercised this against a mocked
// WebSocket; this is the one proof that a real browser's WebSocket
// constructor, the Sec-WebSocket-Protocol bearer fallback, and
// attachEdgeUrl's hardcoded wss:// all actually interoperate with the real
// edge, the same class of gap mcp-operate-scope.spec.ts found a real defect
// in for the sibling JSON-RPC edge.
//
// Opt-in, like the sibling suites: ./run-mcp-attach-session.sh stands up a
// real Postgres, a real erun-backend-api, a real emcp (with dtach installed)
// behind a self-signed TLS front (attachEdgeUrl always dials wss://, so a
// plain-http edge -- the JSON-RPC spec's shortcut -- cannot prove this path),
// and the console dev server -- then sets this gate.
const gated = process.env.ERUN_E2E_CONSOLE_MCP_ATTACH !== '1';
const envName = process.env.E2E_ATTACH_ENV_NAME ?? 'e2e-attach-session';
const attachHostname = process.env.E2E_ATTACH_HOSTNAME ?? '';

// The TLS front terminates a self-signed cert (there is no real CA in this
// harness) -- the same trust decision an operator's browser makes for a real
// deployed edge's CA-issued cert, just not one Chromium extends by default.
test.use({ ignoreHTTPSErrors: true });

test('an operator can attach to a live session over the real WebSocket edge and drive a real shell', async ({
  page,
}) => {
  test.skip(
    gated,
    'opt-in: set ERUN_E2E_CONSOLE_MCP_ATTACH=1 (./run-mcp-attach-session.sh brings up the stack and sets it)',
  );

  await page.goto('/');
  await page
    .getByRole('navigation', { name: 'Console sections' })
    .getByRole('button', { name: 'MCP access' })
    .click();
  await page.getByRole('combobox', { name: 'Environment' }).click();
  await page.getByRole('option', { name: envName }).click();

  // Any scope mints and renders AttachSessionForm regardless -- it mints its
  // own erun:attach-scoped token internally rather than reusing this one.
  await page.getByRole('button', { name: 'Generate MCP token' }).click();
  await expect(page.getByText(`erun-mcp:erun/${envName}`)).toBeVisible();

  await page.getByLabel(/Environment edge hostname/).fill(attachHostname);
  await page.getByLabel(/Session id/).fill('e2e-attach-session-1');
  await page.getByRole('button', { name: 'Attach' }).click();

  // Connected: the form swaps its Attach button for Disconnect. This is the
  // real WebSocket handshake succeeding -- subprotocol-carried bearer token
  // accepted, dtach creating a fresh session (nothing pre-existed under this
  // session id), a real PTY spawned inside the container.
  await expect(page.getByRole('button', { name: 'Disconnect' })).toBeVisible();

  // Drive a real shell: send a line over the socket and watch the PTY's own
  // output come back over the same connection into the scrollback.
  await page.getByLabel('Send a line to the session').fill('echo ATTACH_E2E_MARKER_42');
  await page.getByRole('button', { name: 'Send' }).click();
  await expect(page.getByRole('log', { name: 'Attach session output' })).toContainText(
    'ATTACH_E2E_MARKER_42',
  );

  await page.getByRole('button', { name: 'Disconnect' }).click();
  await expect(page.getByRole('button', { name: 'Attach' })).toBeVisible();
});
