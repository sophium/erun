import type { Locator, Page } from '@playwright/test';

export type ManageTab = 'General' | 'Runtime' | 'AI' | 'Ports' | 'SSH';

// ManageDialog POM. The dialog title is "<tenant>-<environment>", so the
// caller may pass the expected title for a strict match; otherwise the POM
// finds any open dialog under that pattern.
export class ManageDialog {
  constructor(public readonly page: Page, private readonly expectedTitle?: string) {}

  locator(): Locator {
    if (this.expectedTitle) {
      return this.page.getByRole('dialog', { name: this.expectedTitle });
    }
    // The manage dialog title is "<tenant>-<environment>"; pick the
    // open dialog that contains the Save+Cancel footer pair plus a
    // visible General tab — that's the manage surface and not, for
    // example, the activity drawer.
    return this.page
      .getByRole('dialog')
      .filter({ has: this.page.getByRole('tab', { name: /^General/ }) })
      .first();
  }

  async waitForOpen(): Promise<void> {
    await this.locator().waitFor({ state: 'visible' });
  }

  async waitForClosed(): Promise<void> {
    await this.locator().waitFor({ state: 'hidden' });
  }

  tab(name: ManageTab): Locator {
    // TabsTrigger renders with role=tab and an aria-label that includes the
    // tab name (plus ", has unsaved changes" when dirty). Match by partial
    // accessible name so dirty state still resolves. Scope to the manage
    // dialog so we don't collide with the terminal tab strip's tablist.
    return this.locator().getByRole('tab', { name: new RegExp(`^${name}(,|$)`) });
  }

  async selectTab(name: ManageTab): Promise<void> {
    await this.tab(name).click();
  }

  async getActiveTab(): Promise<string> {
    const active = this.locator().locator('[role="tab"][aria-selected="true"]').first();
    const label = await active.getAttribute('aria-label');
    if (!label) return '';
    return label.split(',')[0]?.trim() || '';
  }

  async save(): Promise<void> {
    const button = this.locator().getByRole('button', { name: /^Save( |$|ing)/ });
    await button.scrollIntoViewIfNeeded();
    await button.click();
  }

  async cancel(): Promise<void> {
    const button = this.locator().getByRole('button', { name: 'Cancel' });
    await button.scrollIntoViewIfNeeded();
    await button.click();
  }

  async openDelete(): Promise<void> {
    const button = this.locator().getByRole('button', { name: /^Delete/ });
    await button.scrollIntoViewIfNeeded();
    await button.click();
  }

  async confirmDelete(expected: string): Promise<void> {
    await this.page.locator('#manage-confirmation').fill(expected);
    await this.locator().getByRole('button', { name: /^Delete/ }).click();
  }
}
