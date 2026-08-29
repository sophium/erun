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

  // commentThread locates one thread's own roving-tabindex container
  // (ReviewDetailDialog.Comments.tsx's CommentThread), by the same
  // "Comment thread by <author>" accessible name a screen reader announces.
  commentThread(author: string): Locator {
    return this.locator().getByRole('region', { name: `Comment thread by ${author}` });
  }

  // The "Keyboard shortcuts" popover this dialog's header renders
  // (ReviewKeyboardShortcuts.tsx) -- the same shared affordance the diff
  // panel exposes, so either entry point teaches the whole keyboard model.
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

  // resolveButton/reopenButton are offered only on a thread's root; `threadIndex`
  // is the thread's position among rendered threads (roots), matching reply()'s
  // own indexing convention.
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
    // The picker is a Radix Select, portal'd to the document body.
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
