import { test, expect } from '../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../fixtures/seedRoot.js';

// Issue #437 — hovering a sidebar env row shows a hover card with the env's
// version, the issue it's working on (branch + linked issue title), and its
// current activity, replacing the plain tenant/env tooltip.
//
// The seeded baseline envs are local-agent with a host-side repopath, so the
// card structure and the resolved "Working on" state are deterministic. The
// seeded repo dir is not a git worktree, so the section resolves to an
// availability reason rather than a branch; the populated branch+issue path
// needs a real worktree with a linked issue and stays covered by the Go-side
// working-issue tests.
test.describe('sidebar env hover card', () => {
  test('hovering an env row opens a card with version, working issue, and activity', async ({
    app,
  }) => {
    await app.sidebar.hoverEnvironmentRow(SEED_TENANT, SEED_ENV_ALPHA);

    const card = app.sidebar.envHoverCard(SEED_TENANT, SEED_ENV_ALPHA);
    await expect(card).toBeVisible({ timeout: 6_000 });

    // All three sections are present.
    await expect(card.getByText('Version', { exact: true })).toBeVisible();
    await expect(card.getByText('Working on', { exact: true })).toBeVisible();
    await expect(card.getByText('Activity', { exact: true })).toBeVisible();

    // The working-issue lookup resolves to a non-empty state (a branch, an
    // availability reason, or the transient loading text) — never a blank.
    await expect
      .poll(async () => (await card.locator('dd').nth(1).textContent())?.trim() ?? '')
      .not.toBe('');

    // Issue #462 — whatever it resolves to, it is never the implementation
    // excuse the card used to print for remote envs.
    expect((await card.locator('dd').nth(1).textContent()) ?? '').not.toContain(
      'worktree lives in the pod',
    );
  });

  // Issue #462 — a never-opened env has no pod to be "idle" about: the
  // Activity row must say it is not open, and the Working-on row must offer
  // the next step instead of an implementation excuse. A per-test seeded env
  // is guaranteed never-opened: it did not exist before this test, and the
  // fresh frontend has no tabs for it.
  test('a not-open env shows "Not open", never "Idle" or the pod excuse', async ({
    app,
    seededEnv,
  }) => {
    const { tenant, environment } = seededEnv;

    await app.sidebar.hoverEnvironmentRow(tenant, environment);
    const card = app.sidebar.envHoverCard(tenant, environment);
    await expect(card).toBeVisible({ timeout: 6_000 });

    const activity = card.locator('dd').nth(2);
    await expect(activity).toHaveText('Not open', { timeout: 6_000 });

    const workingOn = card.locator('dd').nth(1);
    await expect.poll(async () => (await workingOn.textContent())?.trim() ?? '').not.toBe('');
    expect((await workingOn.textContent()) ?? '').not.toContain('worktree lives in the pod');
  });

  // Issue #462/#470 — an open env whose real state is stopped must say
  // "Stopped", never "Idle". A real stopped cloud context cannot be staged
  // headless, so the spec drives the env-status event the Go side emits
  // (the emission decisions are owned by erun-ui/env_status_test.go).
  test('an open env flagged stopped shows "Stopped" in the Activity row', async ({
    app,
    page,
    seededEnv,
  }) => {
    const { tenant, environment } = seededEnv;

    await app.sidebar.openEnvironment(tenant, environment);
    const dot = app.sidebar.envOpenDot(tenant, environment);
    await expect(dot).toBeVisible({ timeout: 6_000 });

    // The open click left the pointer on the row, so hovering it again would
    // be a no-op (no fresh mouseenter) — park the pointer elsewhere first.
    await page.mouse.move(0, 0);
    await app.sidebar.hoverEnvironmentRow(tenant, environment);
    const card = app.sidebar.envHoverCard(tenant, environment);
    await expect(card).toBeVisible({ timeout: 6_000 });

    // Let the open settle first: the in-flight open's busy label outranks
    // the stopped state by design, and the spawn path itself emits a
    // clearing env-status — an injection raced against it would be
    // overwritten (which is the production contract, not a flake). The
    // settled state depends on how far the stub-backed `erun open` gets
    // (the kubectl stub fails its deploy probe), so accept any terminal
    // non-busy state before injecting.
    const activity = card.locator('dd').nth(2);
    await expect(activity).toContainText(/Idle|Stopped|Deploy failed|Not open/, {
      timeout: 15_000,
    });

    await emitEnvStatusEvent(page, tenant, environment, 'stopped');
    await expect(dot).toHaveAttribute('data-env-state', 'stopped', { timeout: 4_000 });
    await expect(activity).toContainText('Stopped', { timeout: 4_000 });

    // Restore the row's healthy state and close the env for later specs.
    await emitEnvStatusEvent(page, tenant, environment, '');
    await expect(dot).toHaveAttribute('data-env-state', 'running', { timeout: 4_000 });
    await dot.click();
    await expect(dot).toHaveCount(0, { timeout: 6_000 });
  });
});

// emitEnvStatusEvent injects an `env-status` event, mirroring what the Go
// side emits from tryReconnect's refusal paths and the open/respawn clears.
async function emitEnvStatusEvent(
  page: import('@playwright/test').Page,
  tenant: string,
  environment: string,
  status: string,
): Promise<void> {
  await page.evaluate(
    (payload) => {
      const runtime = (
        window as unknown as {
          runtime: { EventsEmit: (name: string, ...args: unknown[]) => void };
        }
      ).runtime;
      runtime.EventsEmit('env-status', payload);
    },
    { tenant, environment, status },
  );
}
