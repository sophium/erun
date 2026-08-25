import type { Locator, Page } from '@playwright/test';

// CreateReviewDialog is the "Open a review" dialog: pushing the
// selected environment's branch, then creating the review itself.
export class CreateReviewDialog {
  constructor(public readonly page: Page) {}

  locator(): Locator {
    return this.page.getByRole('dialog');
  }

  async waitForOpen(): Promise<void> {
    await this.locator()
      .getByRole('heading', { name: 'Open a review' })
      .waitFor({ state: 'visible' });
  }

  // Scoped to this dialog's own heading, not "no role=dialog at all": a
  // successful create opens the review detail dialog right behind this one
  // closing, and that is a *different* role="dialog" element still visible.
  async waitForClosed(): Promise<void> {
    await this.page.getByRole('heading', { name: 'Open a review' }).waitFor({ state: 'hidden' });
  }

  commitMessageInput(): Locator {
    return this.locator().getByLabel('Commit message');
  }

  nameInput(): Locator {
    return this.locator().getByLabel('Review name');
  }

  // By role, not by label: the field's chevron is labelled "Show target branch
  // choices", which getByLabel('Target branch') also matches.
  targetBranchInput(): Locator {
    return this.locator().getByRole('combobox', { name: /^Target branch/ });
  }

  async openTargetBranchChoices(): Promise<void> {
    await this.locator().getByRole('button', { name: 'Show target branch choices' }).click();
  }

  async fillName(name: string): Promise<void> {
    await this.nameInput().fill(name);
  }

  async fillCommitMessage(message: string): Promise<void> {
    await this.commitMessageInput().fill(message);
  }

  async commit(): Promise<void> {
    await this.locator().getByRole('button', { name: 'Commit' }).click();
  }

  async push(): Promise<void> {
    await this.locator().getByRole('button', { name: 'Push' }).click();
  }

  createButton(): Locator {
    return this.locator().getByRole('button', { name: 'Create review' });
  }

  // The hint that replaces a dead Create button: it names what the review
  // still needs instead of leaving the operator to guess.
  requirementHint(): Locator {
    return this.locator().getByText('Add a name to open the review.');
  }

  async create(): Promise<void> {
    await this.createButton().click();
  }

  async cancel(): Promise<void> {
    await this.locator().getByRole('button', { name: 'Cancel' }).click();
  }
}
