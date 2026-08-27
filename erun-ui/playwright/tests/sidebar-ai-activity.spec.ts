import { expect, test } from '../fixtures/erunApp.js';

// Covers the structured AI-session status model (erun#1105): an env's AI-tab
// badge is driven by the tool's own turn-boundary self-report (busy / idle /
// awaiting-input / unknown), read through the same environment-activity
// observation as the generic busy marker — not inferred from PTY output
// volume, and not a separate ai-activity event the way it used to be (that
// event is orchestrator-only now; see sidebar-orchestrator-busy-snapshot.spec.ts).
//
// The Go-side poller that would produce a real env-activity observation needs
// a live MCP edge the headless harness cannot stand up, so this spec emits the
// Wails event directly and asserts the rendered states. Emitting the same
// event the poller emits (rather than mutating Go state and waiting for a
// poll) is the deterministic way to drive this from a spec.

function emitEnvActivity(
  page: import('@playwright/test').Page,
  payload: {
    tenant: string;
    environment: string;
    reachable: boolean;
    observed: boolean;
    busy?: boolean;
    detail?: string;
    aiState?: string;
    aiTool?: string;
  },
) {
  return page.evaluate((payload) => {
    const runtime = (
      window as unknown as {
        runtime: { EventsEmit: (n: string, ...a: unknown[]) => void };
      }
    ).runtime;
    runtime.EventsEmit('env-activity', { busy: false, ...payload });
  }, payload);
}

test.describe('sidebar AI session status', () => {
  test('a busy AI session surfaces a spinner on the env row, idle clears it', async ({
    page,
    seededEnv,
  }) => {
    const { tenant, environment } = seededEnv;
    const sidebar = page.locator('aside').first();
    const spinner = sidebar.getByRole('status', { name: new RegExp(`${tenant} / ${environment}`) });
    await expect(spinner).toHaveCount(0);

    await emitEnvActivity(page, {
      tenant,
      environment,
      reachable: true,
      observed: true,
      aiState: 'busy',
      aiTool: 'claude',
    });
    await expect(spinner).toBeVisible();
    await expect(spinner).toHaveAttribute('aria-label', /AI tab working/);

    await emitEnvActivity(page, {
      tenant,
      environment,
      reachable: true,
      observed: true,
      aiState: 'idle',
      aiTool: 'claude',
    });
    await expect(spinner).toHaveCount(0);
  });

  // The load-bearing case: a session silently waiting on the operator must not
  // read as idle, and must render distinctly from the ordinary busy spinner —
  // exactly the state a PTY-output-volume heuristic could never produce, since
  // a session waiting on a human and one that finished look identical from
  // output alone.
  test('an awaiting-input session renders distinctly from busy and never reads idle', async ({
    page,
    seededEnv,
  }) => {
    const { tenant, environment } = seededEnv;
    const sidebar = page.locator('aside').first();
    const row = sidebar.getByRole('button', { name: new RegExp(`^${tenant} / ${environment}`) });

    await emitEnvActivity(page, {
      tenant,
      environment,
      reachable: true,
      observed: true,
      aiState: 'awaiting-input',
      aiTool: 'claude',
    });

    // Distinct glyph, not the spinner: BusyRowSpinner carries role="status"
    // with an "AI tab working"/"is busy" label; the awaiting-input glyph
    // carries a "waiting for your input" label instead, and there must be no
    // spinner rendered at the same time.
    await expect(sidebar.getByRole('status', { name: /is busy|AI tab working/ })).toHaveCount(0);
    const awaitingIndicator = row.getByRole('status', { name: /waiting for your input/ });
    await expect(awaitingIndicator).toBeVisible();
  });

  test('an unknown AI state (no structured self-report) does not spin the row', async ({
    page,
    seededEnv,
  }) => {
    const { tenant, environment } = seededEnv;
    const sidebar = page.locator('aside').first();
    const spinner = sidebar.getByRole('status', { name: new RegExp(`${tenant} / ${environment}`) });

    await emitEnvActivity(page, {
      tenant,
      environment,
      reachable: true,
      observed: true,
      aiState: 'unknown',
      aiTool: 'codex',
    });
    await expect(spinner).toHaveCount(0);
  });

  test('closing an env clears a latched AI-session badge', async ({ app, page, seededEnv }) => {
    const { tenant, environment } = seededEnv;
    const sidebar = page.locator('aside').first();
    const envSpinner = sidebar.getByRole('status', {
      name: new RegExp(`${tenant} / ${environment}`),
    });

    // Open the env so it has live tabs and the close dot mounts.
    await app.sidebar.openEnvironment(tenant, environment);
    const closeDot = sidebar.getByRole('button', { name: `Close ${tenant} / ${environment}` });
    await expect(closeDot).toBeVisible();
    await expect(envSpinner).toHaveCount(0);

    await emitEnvActivity(page, {
      tenant,
      environment,
      reachable: true,
      observed: true,
      aiState: 'busy',
      aiTool: 'claude',
    });
    await expect(envSpinner).toBeVisible();

    // Closing the env must clear the observed activity, including the AI
    // badge, without waiting for the next environment-activity poll tick.
    await closeDot.click();
    await expect(envSpinner).toHaveCount(0);
  });
});
