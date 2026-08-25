import type { Locator, Page } from '@playwright/test';

// ReviewDetailDialog is the hosted platform review's own detail surface,
// opened from a row in the tenant dashboard's Reviews tab (#1199). Named
// apart from ReviewPanel (the local diff panel's POM) on purpose — see
// ReviewDetailDialog.tsx's own header comment for why.
export class ReviewDetailDialog {
  constructor(public readonly page: Page) {}

  locator(): Locator {
    return this.page.getByRole('dialog');
  }

  async waitForOpen(): Promise<void> {
    await this.locator().waitFor({ state: 'visible' });
  }

  async waitForClosed(): Promise<void> {
    await this.locator().waitFor({ state: 'hidden' });
  }

  buildRows(): Locator {
    return this.locator().getByRole('listitem');
  }

  async reply(commentIndex: number): Promise<void> {
    await this.locator().getByRole('button', { name: 'Reply' }).nth(commentIndex).click();
  }

  replyInput(): Locator {
    return this.locator().getByLabel('Reply');
  }

  async fillReply(text: string): Promise<void> {
    await this.replyInput().fill(text);
  }

  async sendReply(): Promise<void> {
    await this.locator().getByRole('button', { name: 'Send' }).click();
  }

  async cancelReply(): Promise<void> {
    await this.locator().getByRole('button', { name: 'Cancel' }).click();
  }

  closeReviewButton(): Locator {
    return this.locator().getByRole('button', { name: 'Close review' });
  }

  confirmCloseButton(): Locator {
    return this.locator().getByRole('button', { name: 'Confirm close' });
  }

  closeRestrictedNote(): Locator {
    return this.locator().getByText('You do not have access to close this review.');
  }
}
