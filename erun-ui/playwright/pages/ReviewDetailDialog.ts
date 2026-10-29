import type { Locator, Page } from '@playwright/test';

// The hosted platform review's detail dialog (distinct from ReviewPanel, the local diff panel).
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

  buildProfileButtonFor(buildId: string): Locator {
    return this.locator().getByRole('button', { name: `View build profile for ${buildId}` });
  }

  commentThread(author: string): Locator {
    return this.locator().getByRole('region', { name: `Comment thread by ${author}` });
  }

  keyboardShortcutsButton(): Locator {
    return this.locator().getByRole('button', { name: 'Keyboard shortcuts' });
  }

  keyboardShortcutsPopover(): Locator {
    return this.page.locator('[data-slot="popover-content"]');
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

  // `threadIndex` counts root threads, matching reply().
  resolveButton(threadIndex: number): Locator {
    return this.locator().getByRole('button', { name: 'Resolve' }).nth(threadIndex);
  }

  reopenButton(threadIndex: number): Locator {
    return this.locator().getByRole('button', { name: 'Reopen' }).nth(threadIndex);
  }

  closeReviewButton(): Locator {
    return this.locator().getByRole('button', { name: 'Close review' });
  }

  confirmCloseButton(): Locator {
    return this.locator().getByRole('button', { name: 'Confirm close' });
  }

  closeRestrictedNote(): Locator {
    return this.locator().getByText('You do not have access to close this review.', {
      exact: true,
    });
  }

  reviewerRow(username: string): Locator {
    return this.locator().locator('li').filter({ hasText: username });
  }

  reviewersRestrictedNote(): Locator {
    return this.locator().getByText("You do not have access to this review's reviewers.", {
      exact: false,
    });
  }

  addReviewerButton(): Locator {
    return this.locator().getByRole('button', { name: 'Add reviewer', exact: true });
  }

  reviewerPickerTrigger(): Locator {
    return this.locator().locator('#add-reviewer-user');
  }

  async choosePendingReviewer(username: string): Promise<void> {
    await this.reviewerPickerTrigger().click();
    // Radix Select portals to the document body.
    await this.page.getByRole('option', { name: username }).click();
  }

  assignReviewerButton(): Locator {
    return this.locator().getByRole('button', { name: 'Assign' });
  }

  cancelAddReviewerButton(): Locator {
    return this.locator().getByRole('button', { name: 'Cancel' });
  }

  noReviewersLeftNote(): Locator {
    return this.locator().getByText('Every enrolled user in this tenant is already a reviewer.', {
      exact: false,
    });
  }

  removeReviewerButton(username: string): Locator {
    return this.reviewerRow(username).getByRole('button', { name: 'Remove', exact: true });
  }

  confirmRemoveReviewerButton(username: string): Locator {
    return this.reviewerRow(username).getByRole('button', { name: 'Confirm remove' });
  }
}
