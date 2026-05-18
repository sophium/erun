import type { Locator, Page } from '@playwright/test';

// ActivityQueueDrawer POM. The drawer slides in from the right when its
// launcher (fixed floating button labelled "Open deploy queue") is clicked.
export class ActivityQueueDrawer {
  constructor(public readonly page: Page) {}

  locator(): Locator {
    return this.page.getByRole('dialog', { name: 'Activity queue' });
  }

  launcher(): Locator {
    return this.page.getByRole('button', { name: /^Open deploy queue/ });
  }

  closeButton(): Locator {
    return this.page.getByRole('button', { name: 'Close activity queue' });
  }

  async open(): Promise<void> {
    await this.launcher().click();
    await this.locator().waitFor({ state: 'visible' });
  }

  async close(): Promise<void> {
    await this.closeButton().click();
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
