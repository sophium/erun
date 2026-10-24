import type { Locator, Page } from '@playwright/test';

export class ActivityQueueDrawer {
  constructor(public readonly page: Page) {}

  locator(): Locator {
    return this.page.getByRole('dialog', { name: 'Activity queue' });
  }

  launcher(): Locator {
    return this.page.getByRole('button', { name: /^Open activities/ });
  }

  closeButton(): Locator {
    return this.page.getByRole('button', { name: 'Close activity queue' });
  }

  async open(): Promise<void> {
    await this.launcher().click();
    await this.locator().waitFor({ state: 'visible' });
  }

  // Converges on the drawer having closed, like open().
  async close(): Promise<void> {
    await this.closeButton().click();
    await this.locator().waitFor({ state: 'hidden' });
  }

  async getEntries(): Promise<string[]> {
    const items = this.locator().locator('section [role="status"], section li, section article');
    const count = await items.count();
    const out: string[] = [];
    for (let i = 0; i < count; i++) {
      const text = (await items.nth(i).textContent())?.trim();
      if (text) out.push(text);
    }
    return out;
  }
}
