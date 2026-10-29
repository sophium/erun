import { expect, test, waitForSeededRow } from '../../../fixtures/erunApp.js';
import {
  SEED_TENANT,
  removeEnvironment,
  seedEnvironmentWithRuntimeVersions,
  uniqueEnvironmentName,
} from '../../../fixtures/seedRoot.js';

// The card shows runtime and erun versions separately, and never breaks a value mid-token.

// Resolve by label, not position, since some rows render conditionally.
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
      // A re-render can drop the card mid-read, so hover and reads retry together.
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

  // Chromium reports two rects for an ellipsis run, so assert nowrap and box height instead of
  // getClientRects().
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
      expect(valueHeight).toBeLessThan(labelHeight * 1.5);
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });
});
