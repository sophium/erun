import type { Locator, Page } from '@playwright/test';

// EnvironmentInitDialog drives the "New environment" dialog.
export class EnvironmentInitDialog {
  constructor(public readonly page: Page) {}

  locator(): Locator {
    return this.page.getByRole('dialog', { name: 'New environment' });
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

  containerRegistryInput(): Locator {
    return this.page.locator('#environment-container-registry');
  }

  versionChoicesButton(): Locator {
    return this.page.getByRole('button', { name: 'Show version choices' });
  }

  versionNotices(): Locator {
    return this.page.getByRole('list', { name: 'Version source notices' });
  }

  kubernetesContextTrigger(): Locator {
    return this.page.locator('#environment-kubernetes-context');
  }

  // createButton matches the submit button in both its idle ("Create") and
  // in-flight ("Creating...") copy so callers can assert its enabled state.
  createButton(): Locator {
    return this.locator().getByRole('button', { name: /^(Create|Creating)/ });
  }

  // submitReason is the live-region status line that explains why Create is
  // blocked; it stays mounted (empty) when the dialog is submittable.
  submitReason(): Locator {
    return this.page.locator('#environment-dialog-submit-reason');
  }

  async fillContainerRegistry(value: string): Promise<void> {
    await this.containerRegistryInput().fill(value);
  }

  hostedRegistryCheckbox(): Locator {
    return this.page.locator('#environment-use-erun-registry');
  }

  // selectKubernetesContext opens the context dropdown and picks an option by
  // its label (the context name).
  async selectKubernetesContext(name: string): Promise<void> {
    await this.kubernetesContextTrigger().click();
    await this.page.getByRole('option', { name, exact: true }).click();
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
    const button = this.locator().getByRole('button', {
      name: /^(Create|Creating)/,
    });
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
