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

// Binds a real listener on the env's MCP port that answers GET but never the
// MCP POST; the harness's reachability override must keep opening the env from
// hanging on it. Range starts must sit on erun-common's 100-port grid.
const PORT_RANGE_GRID = 100;
const PINNED_RANGE_BASE = 64000;

// Ports are host-wide and specs run fully parallel, so each worker gets its
// own grid slots.
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
        res.writeHead(200);
        res.end();
        return;
      }
      // Accept the MCP connect but never respond.
    });
    await new Promise<void>((resolve, reject) => {
      listener.once('error', reject);
      listener.listen(rangeStart, '127.0.0.1', resolve);
    });

    try {
      seedEnvironment(SEED_TENANT, environment, `localportrangestart: ${String(rangeStart)}\n`);
      await waitForSeededRow(app, SEED_TENANT, environment);

      await app.sidebar.openEnvironment(SEED_TENANT, environment);

      // The AI tab spawning follows the occupancy check, so wait for it before the
      // negative assertions or they pass vacuously.
      await app.tabStrip.waitForTab('AI');

      await expect(app.aiOccupancyPromptDialog.locator()).toHaveCount(0);

      // A wrongly-opened dialog's overlay would intercept this click.
      await app.titlebar.toggleReviewPanel();
      await expect(page.getByRole('dialog')).toHaveCount(0);
    } finally {
      listener.close();
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  // A real held lease must still raise the occupancy dialog under a port collision.
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
