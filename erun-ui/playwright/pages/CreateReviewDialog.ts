import type { Locator, Page } from '@playwright/test';

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

  // Scoped to this heading: a successful create opens the review detail dialog behind it.
  async waitForClosed(): Promise<void> {
    await this.page.getByRole('heading', { name: 'Open a review' }).waitFor({ state: 'hidden' });
  }

  commitMessageInput(): Locator {
    return this.locator().getByLabel('Commit message');
  }

  nameInput(): Locator {
    return this.locator().getByLabel('Review name');
  }

  // By role: the chevron label also matches getByLabel('Target branch').
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
