import { expect, test, waitForSeededRow } from '../fixtures/erunApp.js';
import {
  SEED_TENANT,
  removeEnvironment,
  seedEnvironmentWithRuntimeVersions,
  uniqueEnvironmentName,
} from '../fixtures/seedRoot.js';

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

async function seedAndOpen(
  app: import('../pages/index.js').AppShell,
  title: string,
  versions: Parameters<typeof seedEnvironmentWithRuntimeVersions>[2],
): Promise<{ environment: string; card: import('@playwright/test').Locator }> {
  const environment = uniqueEnvironmentName(title);
  seedEnvironmentWithRuntimeVersions(SEED_TENANT, environment, versions);
  await waitForSeededRow(app, SEED_TENANT, environment);
  await app.sidebar.hoverEnvironmentRow(SEED_TENANT, environment);
  const card = app.sidebar.envHoverCard(SEED_TENANT, environment);
  await expect(card).toBeVisible();
  return { environment, card };
}

test.describe('sidebar env hover card version rows', () => {
  test('a tenant-line runtime image with an explicit runtime chart shows two distinct version rows', async ({
    app,
  }) => {
    const { environment, card } = await seedAndOpen(app, 'tenant-line', {
      runtimeVersion: '1.0.356-snapshot-20260827091350',
      runtimeRunningImage: 'ghcr.io/sophium/petios-devops:1.0.356-snapshot-20260827091350',
      runtimeChart: 'oci://ghcr.io/sophium/erun-devops:1.0.239',
    });
    try {
      await expect(rowValue(card, 'Version')).toContainText('1.0.356-snapshot-20260827091350');
      await expect(rowValue(card, 'Version')).toContainText('petios line');
      await expect(rowValue(card, 'Version')).toContainText('ghcr.io/sophium/petios-devops');

      await expect(rowValue(card, 'Erun version')).toContainText('1.0.239');
      await expect(rowValue(card, 'Erun version')).not.toContainText('1.0.356');
      await expect(rowValue(card, 'Line mismatch')).toHaveCount(0);
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  test('no resolved runtime image reads the line as undetermined, never as the tenant name', async ({
    app,
  }) => {
    const { environment, card } = await seedAndOpen(app, 'undetermined-line', {
      runtimeVersion: '1.0.239',
    });
    try {
      await expect(rowValue(card, 'Version')).toContainText('Line undetermined');
      await expect(rowValue(card, 'Version')).not.toContainText(SEED_TENANT);

      await expect(rowValue(card, 'Erun version')).toContainText('Undetermined');
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  test("a stock image confirmed on erun's own line does not duplicate the number", async ({
    app,
  }) => {
    const { environment, card } = await seedAndOpen(app, 'erun-line', {
      runtimeVersion: '1.0.239',
      runtimeRunningImage: 'ghcr.io/sophium/erun-devops:1.0.239',
    });
    try {
      await expect(rowValue(card, 'Version')).toContainText('erun line');
      await expect(rowValue(card, 'Erun version')).toContainText('Same as runtime version');
      await expect(rowValue(card, 'Erun version')).not.toContainText('1.0.239');
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  test('a recorded/observed release-line disagreement is surfaced as a warning', async ({
    app,
  }) => {
    const { environment, card } = await seedAndOpen(app, 'line-mismatch', {
      runtimeVersion: '1.0.86',
      runtimeImage: 'ghcr.io/sophium/erun-devops',
      runtimeRunningImage: 'ghcr.io/sophium/frs-devops:1.0.86',
    });
    try {
      await expect(rowValue(card, 'Line mismatch')).toContainText('erun');
      await expect(rowValue(card, 'Line mismatch')).toContainText('frs');
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
    const { environment, card } = await seedAndOpen(app, 'long-version-layout', {
      runtimeVersion: '1.0.356-snapshot-20260831111243',
    });
    try {
      const versionValue = rowValue(card, 'Version').locator('.truncate');
      await expect(versionValue).toBeVisible();
      await expect(versionValue).toHaveAttribute('title', '1.0.356-snapshot-20260831111243');
      const whiteSpace = await versionValue.evaluate(
        (el) => window.getComputedStyle(el).whiteSpace,
      );
      const valueHeight = await versionValue.evaluate((el) => el.getBoundingClientRect().height);
      const labelHeight = await card
        .locator('dt:text-is("Version")')
        .evaluate((el) => el.getBoundingClientRect().height);
      expect(whiteSpace).toBe('nowrap');
      // A wrapped two-line value would be roughly double a single-line
      // label's height; a truncated one-line value matches it.
      expect(valueHeight).toBeLessThan(labelHeight * 1.5);
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });
});
