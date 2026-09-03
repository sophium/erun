import { expect, test } from '@playwright/test';

// Real end-to-end proof for erun#1107 Phase 3 / erun#763 / erun#2024 /
// erun#2026 / erun#2035: a console session minted at erun:operate can drive
// the operate-shaped tools (deploy/context_start/context_stop/resize) over a
// REAL live MCP edge, and is refused every admin-only tool
// (exec_raw/delete/terraform/init) the tier exists to keep it away from --
// the actual blast-radius property those issues exist to hold, proven
// against a real, unmodified erun-mcp binary rather than asserted from
// reading its source.
//
// Opt-in, like the OIDC suite: ./run-mcp-operate-scope.sh stands up a real
// Postgres, a real erun-backend-api (its own MCP signer, no live IdP), a
// real emcp instance in a throwaway rootful container, and the console dev
// server signed in as an ordinary TenantUser -- then sets this gate.
const gated = process.env.ERUN_E2E_CONSOLE_MCP_OPERATE !== '1';
const envName = process.env.E2E_MCP_ENV_NAME ?? 'e2e-operate-scope';
const mcpHostname = process.env.E2E_MCP_HOSTNAME ?? '';

// Every tool erun:operate is refused, mirrored against #763 Phase 3's own
// boundary: none of these can be reached with only erun:operate, by design.
const ADMIN_ONLY_TOOLS = ['exec_raw', 'delete', 'terraform', 'init'];

async function openMcpAccessForEnvironment(page: import('@playwright/test').Page): Promise<void> {
  await page.goto('/');
  await page
    .getByRole('navigation', { name: 'Console sections' })
    .getByRole('button', { name: 'MCP access' })
    .click();
  await page.getByRole('combobox', { name: 'Environment' }).click();
  await page.getByRole('option', { name: envName }).click();
}

test('an erun:operate-scoped console session drives operate tools and is refused every admin-only one', async ({
  page,
}) => {
  test.skip(
    gated,
    'opt-in: set ERUN_E2E_CONSOLE_MCP_OPERATE=1 (./run-mcp-operate-scope.sh brings up the stack and sets it)',
  );

  await openMcpAccessForEnvironment(page);

  // Token capability already defaults to erun:operate (MCPAccessPanel.tsx) --
  // no additional entitlement needed, matching what erun:operate is for.
  await page.getByRole('button', { name: 'Generate MCP token' }).click();
  await expect(page.getByText(`erun-mcp:erun/${envName}`)).toBeVisible();

  // The console never offers a dead end: the Tool dropdown lists only the
  // four tools erun:operate actually grants -- there is no control an
  // operator could click that would ever reach exec_raw/delete/terraform/
  // init through this form.
  await page.getByRole('combobox', { name: 'Tool' }).click();
  await expect(page.getByRole('option')).toHaveText([
    'Deploy — install a published version',
    'Start the cloud context',
    'Stop the cloud context',
    'Resize the runtime pod',
  ]);
  await page.getByRole('option', { name: 'Start the cloud context' }).click();

  await page.getByLabel(/MCP hostname/).fill(mcpHostname);

  // 1. context_start reaches the real handler over the real edge (preview is
  //    on by default) and answers with a real domain-level result -- not a
  //    capability refusal -- proving the operate tier's own capability check
  //    passed.
  await page.getByRole('textbox', { name: 'Cloud context name', exact: false }).fill('e2e-ctx');
  await page.getByRole('button', { name: 'Call the tool' }).click();
  await expect(page.getByText('cloud context "e2e-ctx" is not configured')).toBeVisible();

  // 2. deploy reaches the real handler too, over the same token. Selected by
  //    role (not getByLabel) -- Radix's Select briefly keeps the previous
  //    listbox option in the accessibility tree during its close transition,
  //    which getByLabel's looser accessible-name match can still resolve.
  await page.getByRole('combobox', { name: 'Tool' }).click();
  await page.getByRole('option', { name: 'Deploy — install a published version' }).click();
  await page.getByRole('textbox', { name: 'Version', exact: false }).fill('9.9.9');
  await page.getByRole('button', { name: 'Call the tool' }).click();
  await expect(page.getByText('no such tenant exists: erun')).toBeVisible();

  // 3. The admin-only tools erun:operate must never reach are proven refused
  //    by calling them over the exact live-edge wire protocol liveClient.ts
  //    speaks (initialize -> notifications/initialized -> tools/call), using
  //    the token this session just minted, from inside this same real
  //    browser tab. The console's own form has no control that reaches these
  //    (asserted above); this reproduces what any other MCP client handed
  //    this token would experience -- the actual boundary a lower-trust
  //    caller (erun#1107 Phase 3's mobile client, for one) relies on.
  const token = await page.getByLabel('MCP bearer token').inputValue();
  const outcomes = await page.evaluate(
    async ({ hostname, bearer, tools }) => {
      const url = `${hostname.replace(/\/+$/, '')}/mcp`;
      const headers: Record<string, string> = {
        'Content-Type': 'application/json',
        Accept: 'application/json, text/event-stream',
        Authorization: `Bearer ${bearer}`,
      };
      const initRes = await fetch(url, {
        method: 'POST',
        headers,
        body: JSON.stringify({
          jsonrpc: '2.0',
          id: 1,
          method: 'initialize',
          params: {
            protocolVersion: '2025-06-18',
            capabilities: {},
            clientInfo: { name: 'operate-scope-proof', version: '0' },
          },
        }),
      });
      const sessionId = initRes.headers.get('Mcp-Session-Id') ?? '';
      await fetch(url, {
        method: 'POST',
        headers: { ...headers, 'Mcp-Session-Id': sessionId },
        body: JSON.stringify({ jsonrpc: '2.0', method: 'notifications/initialized' }),
      });
      const results: Record<string, string> = {};
      for (const tool of tools) {
        const res = await fetch(url, {
          method: 'POST',
          headers: { ...headers, 'Mcp-Session-Id': sessionId },
          body: JSON.stringify({
            jsonrpc: '2.0',
            id: 2,
            method: 'tools/call',
            params: { name: tool, arguments: {} },
          }),
        });
        const parsed = (await res.json()) as { error?: { message?: string } };
        results[tool] = parsed.error?.message ?? 'no error -- tool was reachable';
      }
      return results;
    },
    { hostname: mcpHostname, bearer: token, tools: ADMIN_ONLY_TOOLS },
  );

  for (const tool of ADMIN_ONLY_TOOLS) {
    expect(outcomes[tool]).toBe(`unknown tool "${tool}"`);
  }
});

// This is the honest half of the proof: the refusal above ("unknown tool")
// is a JSON-RPC protocol error, not a sentence naming erun:admin -- an
// external MCP client presented with an erun:operate token and no other
// documentation would read that as "this tool does not exist here," not
// "you need a different credential." The console's OWN operator-facing
// refusal is the mint step, not the drive step (an ordinary TenantUser can
// select "Admin" in the scope dropdown, but the backend refuses to mint it
// without the delete-environment entitlement) -- and that refusal used to
// render as a bare "Forbidden" with no next action, exactly the dead end
// root AGENTS.md's "Smooth, Seamless, No Dead Ends" section calls a defect.
// erun-backend-api/internal/routes/mcp_token.go now names the reason and the
// remedy explicitly; this asserts the console renders it end to end.
test('requesting the admin scope with no delete-environment entitlement names the reason, not a bare Forbidden', async ({
  page,
}) => {
  test.skip(
    gated,
    'opt-in: set ERUN_E2E_CONSOLE_MCP_OPERATE=1 (./run-mcp-operate-scope.sh brings up the stack and sets it)',
  );

  await openMcpAccessForEnvironment(page);
  await page.getByRole('combobox', { name: 'Token capability' }).click();
  await page.getByRole('option', { name: /Admin —/ }).click();
  await page.getByRole('button', { name: 'Generate MCP token' }).click();

  await expect(
    page.getByText(
      'Could not mint an MCP token: minting an erun:admin MCP token requires permission to delete this environment; ask your tenant admin for that access, or request erun:operate instead',
    ),
  ).toBeVisible();
});
