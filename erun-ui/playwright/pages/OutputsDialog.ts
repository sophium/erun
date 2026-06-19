import type { Locator, Page } from '@playwright/test';

// OutputsDialog POM — the per-env "Agent outputs" dialog that lists the files
// an agent produced in the runtime pod and offers a per-row Download.
export class OutputsDialog {
  constructor(public readonly page: Page) {}

  locator(): Locator {
    return this.page.getByRole('dialog').filter({ hasText: 'Agent outputs' });
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
