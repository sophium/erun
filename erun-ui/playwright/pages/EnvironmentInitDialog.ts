import type { Locator, Page } from '@playwright/test';

// EnvironmentInitDialog drives the "New environment" / "Deploy environment"
// dialog. The dialog title is "New environment" when initialising and
// "Deploy environment" when deploying; this POM defaults to the
// init variant.
export class EnvironmentInitDialog {
  constructor(public readonly page: Page) {}

  locator(): Locator {
    return this.page.getByRole('dialog', { name: /New environment|Deploy environment/ });
  }

  async waitForOpen(): Promise<void> {
    await this.locator().waitFor({ state: 'visible' });
  }

  async waitForClosed(): Promise<void> {
    await this.locator().waitFor({ state: 'hidden' });
  }

  tenantInput(): Locator {
    return this.page.locator('#environment-tenant');
  }

  environmentInput(): Locator {
    return this.page.locator('#environment-name');
  }

  runtimeVersionInput(): Locator {
    return this.page.locator('#environment-version');
  }

  async fillTenant(name: string): Promise<void> {
    await this.tenantInput().fill(name);
  }

  async fillEnvironment(name: string): Promise<void> {
    await this.environmentInput().fill(name);
  }

  async fillRuntimeVersion(version: string): Promise<void> {
    await this.runtimeVersionInput().fill(version);
  }

  async submit(): Promise<void> {
    const button = this.locator().getByRole('button', { name: /^(Create|Deploy|Creating|Deploying)/ });
    await button.scrollIntoViewIfNeeded();
    await button.click();
  }

  async cancel(): Promise<void> {
    const button = this.locator().getByRole('button', { name: 'Cancel' });
    await button.scrollIntoViewIfNeeded();
    await button.click();
  }

  async getKubernetesContextStatus(): Promise<string> {
    // Both the populated and empty states render text near the label; just
    // capture whatever the field area currently says.
    const field = this.page.locator('#environment-kubernetes-context');
    if (await field.isVisible().catch(() => false)) {
      return (await field.textContent()) || '';
    }
    // No-contexts empty state renders an EmptyState body; read the
    // dialog text as a fallback.
    return (await this.locator().textContent()) || '';
  }
}
