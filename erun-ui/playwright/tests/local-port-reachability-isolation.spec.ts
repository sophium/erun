import * as http from 'node:http';

import { test, expect } from '../fixtures/erunApp.js';
import {
  SEED_TENANT,
  removeEnvironment,
  removeHeldLease,
  seedEnvironment,
  uniqueEnvironmentName,
  writeHeldLease,
} from '../fixtures/seedRoot.js';

// A seeded local-agent env's local port range is computed purely from the
// isolated config store's own tenant/env list (erun-common/ports.go
// ResolveAllEnvironmentLocalPorts), so the port numbers themselves are a
// plain host-wide TCP resource outside HOME/XDG isolation — see
// manage-cloud-alias-local-agent.spec.ts, which already works around this by
// picking a range "no real env allocates". This spec goes further: it binds
// a REAL listener on the env's pinned MCP port before opening it, so the
// occupancy/idle-status probe has something genuine to (wrongly) treat as
// this env's own runtime: a plain GET (the reachability probe) is answered
// fast, but the follow-up MCP session connect (a POST) is accepted and never
// answered — the shape of a real, unrelated environment's edge on that port,
// as opposed to nothing listening at all. Unforced, canReachMCPEndpoint's
// probe succeeds on the GET, so the code proceeds to the real MCP connect,
// which then hangs forever on context.Background() (no deadline) — this is
// what let opening the environment hang until the suite's own timeout instead
// of finishing. ERUN_LOCAL_PORT_REACHABILITY_OVERRIDE (erun-ui/app.go
// withDefaultReachabilityDeps, wired for this harness in
// fixtures/seedRoot.ts backendEnv) forces every such probe to a fixed "not
// reachable" answer, so the code never reaches the real connect at all.
//
// Pinned range starts must land on the 100-port grid erun-common/ports.go
// enforces (offset from LowerServicePort divisible by EnvironmentPortRangeSize),
// or ResolveAllEnvironmentLocalPorts rejects the whole allocation and the env
// never resolves at all — keep every PINNED_RANGE_START below a multiple of 100.
const PINNED_RANGE_START = 64000;

test.describe('local port isolation from a real, unrelated listener', () => {
  test('opening an environment whose port range collides with a genuinely bound port is unaffected: no occupancy dialog, no stray overlay, no hang', async ({
    app,
    page,
  }) => {
    const environment = uniqueEnvironmentName('port-collide');
    const listener = http.createServer((req, res) => {
      if (req.method === 'GET') {
        // Answers the plain reachability probe fast, like a real edge would.
        res.writeHead(200);
        res.end();
        return;
      }
      // The real MCP session connect (a POST): accept the connection but
      // never respond, the way a genuinely unrelated (or stale) edge would.
    });
    await new Promise<void>((resolve, reject) => {
      listener.once('error', reject);
      listener.listen(PINNED_RANGE_START, '127.0.0.1', resolve);
    });

    try {
      seedEnvironment(SEED_TENANT, environment, `localportrangestart: ${PINNED_RANGE_START}\n`);
      await app.reloadEnvironments();
      await app.sidebar
        .envRowButton(SEED_TENANT, environment)
        .waitFor({ state: 'visible', timeout: 15_000 });

      await app.sidebar.openEnvironment(SEED_TENANT, environment);

      // The AI tab starts normally: no occupancy dialog reads the unrelated
      // listener as another job already working here.
      await expect(app.aiOccupancyPromptDialog.locator()).toHaveCount(0);
      const aiTab = page.getByRole('tab', { name: 'AI', exact: true });
      await aiTab.waitFor({ state: 'visible', timeout: 20_000 });

      // No stray overlay from a wrongly-opened dialog blocks a later click:
      // a dialog-overlay intercepting pointer events on the titlebar.
      await app.titlebar.toggleReviewPanel();
      await expect(page.getByRole('dialog')).toHaveCount(0);
    } finally {
      listener.close();
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  // erun#1362: on a host where the seeded env's port range collides with a
  // real listener, the same hang that swallowed the false-occupancy overlay
  // above (canReachMCPEndpoint's GET succeeds, so the code used to proceed to
  // a real, never-answered MCP connect) sat in front of every default tab
  // spawn — including the AI tab's occupancy check, so a genuinely held
  // lease's dialog never got a chance to open at all. This proves the guard
  // still trips under that exact adverse condition, not just that a false one
  // is suppressed.
  const PINNED_RANGE_START_WITH_LEASE = 64100;
  const OCCUPANT_LEASE = 'job-fix-1221-under-collision';

  test('a genuinely held lease still surfaces the occupancy dialog when the port range collides with a real listener', async ({
    app,
    page,
  }) => {
    const environment = uniqueEnvironmentName('port-collide-occupied');
    const listener = http.createServer((req, res) => {
      if (req.method === 'GET') {
        res.writeHead(200);
        res.end();
        return;
      }
      // Never answers, like the false-occupancy case above.
    });
    await new Promise<void>((resolve, reject) => {
      listener.once('error', reject);
      listener.listen(PINNED_RANGE_START_WITH_LEASE, '127.0.0.1', resolve);
    });

    try {
      seedEnvironment(
        SEED_TENANT,
        environment,
        `localportrangestart: ${PINNED_RANGE_START_WITH_LEASE}\n`,
      );
      writeHeldLease(SEED_TENANT, environment, OCCUPANT_LEASE);
      await app.reloadEnvironments();
      await app.sidebar
        .envRowButton(SEED_TENANT, environment)
        .waitFor({ state: 'visible', timeout: 15_000 });

      await app.sidebar.openEnvironment(SEED_TENANT, environment);

      const dialog = app.aiOccupancyPromptDialog;
      await dialog.waitForOpen();
      await expect(dialog.locator()).toContainText(OCCUPANT_LEASE);

      await dialog.cancel();
      await dialog.waitForClosed();
      await expect(page.getByRole('tab', { name: 'AI', exact: true })).toHaveCount(0);
    } finally {
      listener.close();
      removeHeldLease(SEED_TENANT, environment, OCCUPANT_LEASE);
      removeEnvironment(SEED_TENANT, environment);
    }
  });
});
