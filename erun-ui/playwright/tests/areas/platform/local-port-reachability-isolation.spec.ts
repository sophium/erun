import * as http from 'node:http';

import { expect, test, waitForSeededRow } from '../../../fixtures/erunApp.js';
import {
  SEED_TENANT,
  removeEnvironment,
  removeHeldLease,
  seedEnvironment,
  uniqueEnvironmentName,
  writeHeldLease,
} from '../../../fixtures/seedRoot.js';

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
// never resolves at all — so every start below steps in whole grid cells.
const PORT_RANGE_GRID = 100;
const PINNED_RANGE_BASE = 64000;

// A pinned range start is a real, host-wide TCP resource, and this file's
// listeners are bound on it. A constant here is a port two workers can hold at
// once: `fullyParallel: true` (playwright.config.ts) puts this file's two tests —
// and every `--repeat-each` instance of either — in different workers
// concurrently, so the second binder dies on
// `listen EADDRINUSE: address already in use 127.0.0.1:64100` before reaching
// anything it asserts. That is the flake this derivation removes.
//
// Derive the start from the worker instead, using the same worker-unique
// mechanism fixtures/workerBackend.ts already uses for each worker's headless
// backend port: `parallelIndex` is stable across a worker's restarts and
// distinct for every concurrently-running worker (Playwright guarantees the
// same worker never runs two tests at once, so a worker's own slot is free
// again by the time its next test takes it — and the previous listener is
// closed in `finally`). One grid slot per test in this file keeps the two tests
// apart when their instances are split across workers too.
//
// This is a fixed, worker-indexed allocation rather than a dynamic claim: a
// foreign process already holding the port still fails the bind loudly, which
// is the correct outcome for a spec whose whole subject is a real listener.
const SLOTS_PER_WORKER = 2;

function pinnedRangeStart(slot: number): number {
  const { parallelIndex } = test.info();
  return PINNED_RANGE_BASE + (parallelIndex * SLOTS_PER_WORKER + slot) * PORT_RANGE_GRID;
}

test.describe('local port isolation from a real, unrelated listener', () => {
  test('opening an environment whose port range collides with a genuinely bound port is unaffected: no occupancy dialog, no stray overlay, no hang', async ({
    app,
    page,
  }) => {
    const environment = uniqueEnvironmentName('port-collide');
    const rangeStart = pinnedRangeStart(0);
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
      listener.listen(rangeStart, '127.0.0.1', resolve);
    });

    try {
      seedEnvironment(SEED_TENANT, environment, `localportrangestart: ${String(rangeStart)}\n`);
      await waitForSeededRow(app, SEED_TENANT, environment);

      await app.sidebar.openEnvironment(SEED_TENANT, environment);

      // The AI tab spawning is the completion signal both negative assertions
      // below are about, so it comes first. The occupancy check gates that
      // spawn: until the tab is there nothing has run that could have opened
      // the dialog, and a bare `toHaveCount(0)` asserted into that window
      // passes on the instant before the element it names could have rendered
      // -- and goes on passing if that element is rendered a moment later.
      await app.tabStrip.waitForTab('AI');

      // With the check behind us: no occupancy dialog read the unrelated
      // listener as another job already working here ...
      await expect(app.aiOccupancyPromptDialog.locator()).toHaveCount(0);

      // ... and no stray overlay from a wrongly-opened dialog blocks a later
      // click: a dialog-overlay intercepting pointer events on the titlebar.
      await app.titlebar.toggleReviewPanel();
      await expect(page.getByRole('dialog')).toHaveCount(0);
    } finally {
      listener.close();
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  // On a host where the seeded env's port range collides with a
  // real listener, the same hang that swallowed the false-occupancy overlay
  // above (canReachMCPEndpoint's GET succeeds, so the code used to proceed to
  // a real, never-answered MCP connect) sat in front of every default tab
  // spawn — including the AI tab's occupancy check, so a genuinely held
  // lease's dialog never got a chance to open at all. This proves the guard
  // still trips under that exact adverse condition, not just that a false one
  // is suppressed.
  const OCCUPANT_LEASE = 'job-fix-1221-under-collision';

  test('a genuinely held lease still surfaces the occupancy dialog when the port range collides with a real listener', async ({
    app,
    page,
  }) => {
    const environment = uniqueEnvironmentName('port-collide-occupied');
    const rangeStart = pinnedRangeStart(1);
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
      listener.listen(rangeStart, '127.0.0.1', resolve);
    });

    try {
      seedEnvironment(SEED_TENANT, environment, `localportrangestart: ${String(rangeStart)}\n`);
      writeHeldLease(SEED_TENANT, environment, OCCUPANT_LEASE);
      await waitForSeededRow(app, SEED_TENANT, environment);

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
