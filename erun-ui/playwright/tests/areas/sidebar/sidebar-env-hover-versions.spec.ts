import { expect, test, waitForSeededRow } from '../../../fixtures/erunApp.js';
import {
  SEED_TENANT,
  removeEnvironment,
  seedEnvironmentWithRuntimeVersions,
  uniqueEnvironmentName,
} from '../../../fixtures/seedRoot.js';

// The env hover card used to show one bare "Version" row, so a tenant-line
// runtime version (e.g. petios's own release line) read as if it were
// erun's, and the erun version was never shown at all. Its longest value
// also used to break mid-token across two ragged lines, because the narrow
// label-sharing column, not the card's `wide` layout, held it.

// rowValue resolves a HoverCardRow's dd by its dt's exact label text, rather
// than a positional dd index -- robust against rows that only render
// conditionally (Erun version, Line mismatch).
function rowValue(card: import('@playwright/test').Locator, label: string) {
  return card.locator(`dt:text-is("${label}") + dd`);
}

async function seedRow(
  app: import('../../../pages/index.js').AppShell,
  title: string,
  versions: Parameters<typeof seedEnvironmentWithRuntimeVersions>[2],
): Promise<string> {
  const environment = uniqueEnvironmentName(title);
  seedEnvironmentWithRuntimeVersions(SEED_TENANT, environment, versions);
  await waitForSeededRow(app, SEED_TENANT, environment);
  return environment;
}

test.describe('sidebar env hover card version rows', () => {
  test('a tenant-line runtime image with an explicit runtime chart shows two distinct version rows', async ({
    app,
  }) => {
    const environment = await seedRow(app, 'tenant-line', {
      runtimeVersion: '1.0.356-snapshot-20260827091350',
      runtimeRunningImage: 'ghcr.io/sophium/petios-devops:1.0.356-snapshot-20260827091350',
      runtimeChart: 'oci://ghcr.io/sophium/erun-devops:1.0.239',
    });
    try {
      // Hover and every read live inside one retryable block: a re-render
      // (e.g. the periodic activity/usage sweep) can drop the card while the
      // pointer still rests on the row, and a plain un-retried assertion
      // after it reopens does not survive that (erun-ui/playwright/AGENTS.md's
      // hover-card bullet).
      await app.sidebar.readEnvHoverCard(SEED_TENANT, environment, async (card) => {
        await expect(card).toBeVisible({ timeout: 1_000 });
        await expect(rowValue(card, 'Version')).toContainText('1.0.356-snapshot-20260827091350', {
          timeout: 1_000,
        });
        await expect(rowValue(card, 'Version')).toContainText('petios line', { timeout: 1_000 });
        await expect(rowValue(card, 'Version')).toContainText('ghcr.io/sophium/petios-devops', {
          timeout: 1_000,
        });

        await expect(rowValue(card, 'Erun version')).toContainText('1.0.239', { timeout: 1_000 });
        await expect(rowValue(card, 'Erun version')).not.toContainText('1.0.356', {
          timeout: 1_000,
        });
        await expect(rowValue(card, 'Line mismatch')).toHaveCount(0, { timeout: 1_000 });
      });
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  test('no resolved runtime image reads the line as undetermined, never as the tenant name', async ({
    app,
  }) => {
    const environment = await seedRow(app, 'undetermined-line', {
      runtimeVersion: '1.0.239',
    });
    try {
      await app.sidebar.readEnvHoverCard(SEED_TENANT, environment, async (card) => {
        await expect(card).toBeVisible({ timeout: 1_000 });
        await expect(rowValue(card, 'Version')).toContainText('Line undetermined', {
          timeout: 1_000,
        });
        await expect(rowValue(card, 'Version')).not.toContainText(SEED_TENANT, {
          timeout: 1_000,
        });

        await expect(rowValue(card, 'Erun version')).toContainText('Undetermined', {
          timeout: 1_000,
        });
      });
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  test("a stock image confirmed on erun's own line does not duplicate the number", async ({
    app,
  }) => {
    const environment = await seedRow(app, 'erun-line', {
      runtimeVersion: '1.0.239',
      runtimeRunningImage: 'ghcr.io/sophium/erun-devops:1.0.239',
    });
    try {
      await app.sidebar.readEnvHoverCard(SEED_TENANT, environment, async (card) => {
        await expect(card).toBeVisible({ timeout: 1_000 });
        await expect(rowValue(card, 'Version')).toContainText('erun line', { timeout: 1_000 });
        await expect(rowValue(card, 'Erun version')).toContainText('Same as runtime version', {
          timeout: 1_000,
        });
        await expect(rowValue(card, 'Erun version')).not.toContainText('1.0.239', {
          timeout: 1_000,
        });
      });
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  test('a recorded/observed release-line disagreement is surfaced as a warning', async ({
    app,
  }) => {
    const environment = await seedRow(app, 'line-mismatch', {
      runtimeVersion: '1.0.86',
      runtimeImage: 'ghcr.io/sophium/erun-devops',
      runtimeRunningImage: 'ghcr.io/sophium/frs-devops:1.0.86',
    });
    try {
      await app.sidebar.readEnvHoverCard(SEED_TENANT, environment, async (card) => {
        await expect(card).toBeVisible({ timeout: 1_000 });
        await expect(rowValue(card, 'Line mismatch')).toContainText('erun', { timeout: 1_000 });
        await expect(rowValue(card, 'Line mismatch')).toContainText('frs', { timeout: 1_000 });
      });
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  // The longest literal identifier on the card used to sit in the narrow
  // value column it shares with the label, so a version this long broke
  // mid-token across two lines -- the `wide` layout (col-spanning both grid
  // columns) existed to prevent exactly that. #1901 retired `wide` from this
  // card and truncates identifiers instead (with the full value in `title`),
  // which is a stronger guarantee: `white-space: nowrap` makes a mid-token
  // break structurally impossible, not merely unlikely at this card's width.
  //
  // This is asserted via the computed style and the rendered box height
  // rather than `Range.getClientRects()`: Chromium reports TWO overlapping
  // rects (same origin, different widths -- the full logical extent and the
  // ellipsis-clipped visible extent) for a single-line `text-overflow:
  // ellipsis` run, so a rect count is not a reliable one-line signal once
  // ellipsis is involved, only for plain wrapped text. `white-space: nowrap`
  // is the actual guarantee; the single-line box height confirms it did not
  // wrap in practice, not just in principle.
  test('a long snapshot version renders on one line, not broken mid-token', async ({ app }) => {
    const environment = await seedRow(app, 'long-version-layout', {
      runtimeVersion: '1.0.356-snapshot-20260831111243',
    });
    try {
      let whiteSpace = '';
      let valueHeight = 0;
      let labelHeight = 0;
      await app.sidebar.readEnvHoverCard(SEED_TENANT, environment, async (card) => {
        const versionValue = rowValue(card, 'Version').locator('.truncate');
        await expect(versionValue).toBeVisible({ timeout: 1_000 });
        await expect(versionValue).toHaveAttribute('title', '1.0.356-snapshot-20260831111243', {
          timeout: 1_000,
        });
        whiteSpace = await versionValue.evaluate(
          (el) => window.getComputedStyle(el).whiteSpace,
          undefined,
          { timeout: 1_000 },
        );
        valueHeight = await versionValue.evaluate(
          (el) => el.getBoundingClientRect().height,
          undefined,
          {
            timeout: 1_000,
          },
        );
        labelHeight = await card
          .locator('dt:text-is("Version")')
          .evaluate((el) => el.getBoundingClientRect().height, undefined, { timeout: 1_000 });
      });
      expect(whiteSpace).toBe('nowrap');
      // A wrapped two-line value would be roughly double a single-line
      // label's height; a truncated one-line value matches it.
      expect(valueHeight).toBeLessThan(labelHeight * 1.5);
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });
});
