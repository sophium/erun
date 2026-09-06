import type { Page } from '@playwright/test';

import { expect, test } from '../../../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../../../fixtures/seedRoot.js';

// An investigation is a full AI agent on the shared account, so the backend
// refuses to spawn one for a report carrying no diagnostic content (#1029). This
// asserts the observable consequence of that refusal through the same bridge the
// React tree calls: the error the operator is shown, and a population that did
// not grow.
//
// Only the refusing side is exercised here. The admitting side spawns a real
// `claude` process — the harness stubs kubectl/helm/docker/aws but not the AI
// tool — so a spec that drove it would leave an agent behind on every run. The
// bounds that need a live investigation first (one per failure event, the
// per-signature cooldown, the concurrency cap, and the lifetime termination) are
// covered against a stubbed terminal in erun-ui/investigation_bounds_test.go.

// The desktop backend as the browser sees it, narrowed to the two calls here.
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

    // "deploy blew up" is verbatim one of the reports that reached an agent in
    // the environment whose agent quota this exhausted.
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

    // The refusal names the missing evidence and where the real gap is, so the
    // operator has something to act on rather than a silent no-op (Nielsen #9).
    expect(refusal).toContain('no diagnostic content');
    expect(refusal).toContain('failure reporting');

    // The population did not grow: no transient investigator was started.
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
