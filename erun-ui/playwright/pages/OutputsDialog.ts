import type { Locator, Page } from '@playwright/test';

// OutputsDialog POM — the dialog that lists the files an agent produced and
// offers a per-row Download. It serves both producers: an environment's agent
// (in the runtime pod) and an orchestrator (on this host). Its title names which
// one, so the POM keys on a stable test id rather than the wording.
export class OutputsDialog {
  constructor(public readonly page: Page) {}

  locator(): Locator {
    return this.page.getByTestId('outputs-dialog');
  }

  // The list's accessible name is what a screen reader announces over the
  // entries, so it has to name the same producer the heading does.
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
