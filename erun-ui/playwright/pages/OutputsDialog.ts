import type { Locator, Page } from '@playwright/test';

// Serves both agent and orchestrator outputs, so it keys on a test id rather than the title.
export class OutputsDialog {
  constructor(public readonly page: Page) {}

  locator(): Locator {
    return this.page.getByTestId('outputs-dialog');
  }

  list(name: string): Locator {
    return this.locator().getByRole('list', { name });
  }

  entry(name: string): Locator {
    return this.locator().getByText(name, { exact: true });
  }

  downloadButton(name: string): Locator {
    return this.locator().getByRole('button', { name: `Download ${name}` });
  }

  emptyState(): Locator {
    return this.locator().getByText('No outputs yet.', { exact: false });
  }

  status(): Locator {
    return this.locator().getByRole('status');
  }
}
