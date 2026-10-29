import type { Page } from '@playwright/test';

import { expect, test } from '../../../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../../../fixtures/seedRoot.js';

// Only the refusing side is exercised: admitting would spawn a real `claude`
// agent; the other bounds are covered in investigation_bounds_test.go.

interface InvestigateBridge {
  go: {
    main: {
      App: {
        InvestigateFailure: (
          report: string,
          tenant: string,
          environment: string,
          cols: number,
          rows: number,
        ) => Promise<unknown>;
        ListOrchestrators: () => Promise<{ name: string }[] | null>;
      };
    };
  };
}

test.describe('failure auto-investigation bounds', () => {
  test('a report with no diagnostic content is refused and spawns no orchestrator', async ({
    app,
  }) => {
    const before = await listOrchestratorNames(app.page);

    // A real report with no diagnostic content.
    const refusal = await app.page.evaluate(
      async (target: { tenant: string; environment: string }) => {
        const bridge = window as unknown as InvestigateBridge;
        try {
          await bridge.go.main.App.InvestigateFailure(
            'deploy blew up',
            target.tenant,
            target.environment,
            80,
            24,
          );
          return '';
        } catch (error) {
          return error instanceof Error ? error.message : String(error);
        }
      },
      { tenant: SEED_TENANT, environment: SEED_ENV_ALPHA },
    );

    expect(refusal).toContain('no diagnostic content');
    expect(refusal).toContain('failure reporting');

    expect(await listOrchestratorNames(app.page)).toEqual(before);
    await expect(
      app.sidebar.orchestratorStatusDot(`Investigate ${SEED_ENV_ALPHA}`, 'running'),
    ).toHaveCount(0);
  });
});

async function listOrchestratorNames(page: Page): Promise<string[]> {
  const names = await page.evaluate(async () => {
    const bridge = window as unknown as InvestigateBridge;
    const listed = await bridge.go.main.App.ListOrchestrators();
    return (listed ?? []).map((entry) => entry.name);
  });
  return names.sort();
}
