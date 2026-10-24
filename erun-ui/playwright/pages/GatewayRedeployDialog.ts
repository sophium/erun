import type { Locator, Page } from '@playwright/test';

// Offered after a settings save when running envs still route Claude Code the old way.
export class GatewayRedeployDialog {
  constructor(public readonly page: Page) {}

  locator(): Locator {
    return this.page.getByTestId('gateway-redeploy-dialog');
  }

  table(): Locator {
    return this.locator().getByRole('table', { name: 'Environments to redeploy' });
  }

  row(environment: string): Locator {
    return this.table().getByRole('row').filter({ hasText: environment });
  }

  async waitForOpen(): Promise<void> {
    await this.locator().waitFor({ state: 'visible' });
  }

  async waitForClosed(): Promise<void> {
    await this.locator().waitFor({ state: 'hidden' });
  }

  async redeploy(): Promise<void> {
    await this.locator()
      .getByRole('button', { name: /^Redeploy/ })
      .click();
  }

  async later(): Promise<void> {
    await this.locator().getByRole('button', { name: 'Later', exact: true }).click();
  }
}
