import type { Locator, Page } from '@playwright/test';

// Create/edit dialog for a host-side orchestrator; the title differs per mode.
export class OrchestratorDialog {
  constructor(public readonly page: Page) {}

  locator(mode: 'New orchestrator' | 'Edit orchestrator' = 'New orchestrator'): Locator {
    return this.page.getByRole('dialog', { name: mode });
  }

  async waitForOpen(
    mode: 'New orchestrator' | 'Edit orchestrator' = 'New orchestrator',
  ): Promise<void> {
    await this.locator(mode).waitFor({ state: 'visible' });
  }

  async waitForClosed(
    mode: 'New orchestrator' | 'Edit orchestrator' = 'New orchestrator',
  ): Promise<void> {
    await this.locator(mode).waitFor({ state: 'hidden' });
  }

  // Callers re-checking a row after reopening in Edit mode must pass the mode.
  envRow(
    tenant: string,
    environment: string,
    mode: 'New orchestrator' | 'Edit orchestrator' = 'New orchestrator',
  ): Locator {
    return this.locator(mode)
      .locator('label')
      .filter({ hasText: `${tenant} / ${environment}` });
  }

  envCheckbox(
    tenant: string,
    environment: string,
    mode: 'New orchestrator' | 'Edit orchestrator' = 'New orchestrator',
  ): Locator {
    return this.envRow(tenant, environment, mode).getByRole('checkbox');
  }

  async toggleEnv(tenant: string, environment: string): Promise<void> {
    await this.envCheckbox(tenant, environment).click();
  }

  envBlock(
    tenant: string,
    environment: string,
    mode: 'New orchestrator' | 'Edit orchestrator' = 'New orchestrator',
  ): Locator {
    return this.locator(mode)
      .locator('div')
      .filter({ hasText: `${tenant} / ${environment}` })
      .last();
  }

  // Only a mirrored env has an editable directory; a local-agent one is text.
  envDirectoryInput(tenant: string, environment: string): Locator {
    return this.envBlock(tenant, environment).getByRole('textbox');
  }

  envChooseDirectoryButton(tenant: string, environment: string): Locator {
    return this.locator().getByRole('button', {
      name: `Choose sync directory for ${tenant} / ${environment}`,
    });
  }

  directoriesAddButton(
    mode: 'New orchestrator' | 'Edit orchestrator' = 'New orchestrator',
  ): Locator {
    return this.locator(mode).getByRole('button', { name: 'Add directory…' });
  }

  directoryRow(
    directory: string,
    mode: 'New orchestrator' | 'Edit orchestrator' = 'New orchestrator',
  ): Locator {
    return this.locator(mode).getByText(directory, { exact: true });
  }

  directoryRemoveButton(
    directory: string,
    mode: 'New orchestrator' | 'Edit orchestrator' = 'New orchestrator',
  ): Locator {
    return this.locator(mode).getByRole('button', { name: `Remove ${directory}` });
  }

  // Id matches OrchestratorDialog.Environments.helpers.ts's envRoleFieldId.
  envRoleTrigger(tenant: string, environment: string): Locator {
    return this.page.locator(`#orchestrator-env-role-${tenant}-${environment}`);
  }

  aliasTrigger(mode: 'New orchestrator' | 'Edit orchestrator' = 'New orchestrator'): Locator {
    return this.locator(mode).locator('#orchestrator-alias');
  }

  async setAlias(
    alias: string,
    mode: 'New orchestrator' | 'Edit orchestrator' = 'New orchestrator',
  ): Promise<void> {
    await this.aliasTrigger(mode).click();
    await this.page.getByRole('option', { name: alias, exact: true }).click();
  }

  // Read as a list: specs assert which aliases are absent as well as present.
  async aliasOptionNames(
    mode: 'New orchestrator' | 'Edit orchestrator' = 'New orchestrator',
  ): Promise<string[]> {
    await this.aliasTrigger(mode).click();
    const options = this.page.getByRole('option');
    await options.first().waitFor({ state: 'visible' });
    const names = await options.allInnerTexts();
    await this.page.keyboard.press('Escape');
    return names.map((name) => name.trim());
  }

  // The model field is a SelectField through the gateway and a textbox off it, under one id.
  gatewayCheckbox(mode: 'New orchestrator' | 'Edit orchestrator' = 'New orchestrator'): Locator {
    return this.locator(mode).getByRole('checkbox', { name: 'Use gateway', exact: true });
  }

  gatewayHelp(mode: 'New orchestrator' | 'Edit orchestrator' = 'New orchestrator'): Locator {
    return this.locator(mode).locator('#orchestrator-gateway-help');
  }

  gatewayRetryButton(mode: 'New orchestrator' | 'Edit orchestrator' = 'New orchestrator'): Locator {
    return this.locator(mode).getByRole('button', { name: 'Retry', exact: true });
  }

  // Lets a spec assert a message is reported once rather than echoed.
  textOccurrences(
    text: string,
    mode: 'New orchestrator' | 'Edit orchestrator' = 'New orchestrator',
  ): Locator {
    return this.locator(mode).getByText(text);
  }

  modelInput(mode: 'New orchestrator' | 'Edit orchestrator' = 'New orchestrator'): Locator {
    return this.locator(mode).getByRole('textbox', { name: 'Model', exact: true });
  }

  modelHelp(mode: 'New orchestrator' | 'Edit orchestrator' = 'New orchestrator'): Locator {
    return this.locator(mode).locator('#orchestrator-model-help');
  }

  modelResetButton(mode: 'New orchestrator' | 'Edit orchestrator' = 'New orchestrator'): Locator {
    return this.locator(mode).getByRole('button', { name: 'Reset to default', exact: true });
  }

  modelTrigger(mode: 'New orchestrator' | 'Edit orchestrator' = 'New orchestrator'): Locator {
    return this.locator(mode).getByRole('combobox', { name: 'Model', exact: true });
  }

  async setGatewayModel(
    label: string,
    mode: 'New orchestrator' | 'Edit orchestrator' = 'New orchestrator',
  ): Promise<void> {
    await this.modelTrigger(mode).click();
    await this.page.getByRole('option', { name: label, exact: true }).click();
  }

  async gatewayModelOptionNames(
    mode: 'New orchestrator' | 'Edit orchestrator' = 'New orchestrator',
  ): Promise<string[]> {
    await this.modelTrigger(mode).click();
    const options = this.page.getByRole('option');
    await options.first().waitFor({ state: 'visible' });
    const names = await options.allInnerTexts();
    await this.page.keyboard.press('Escape');
    return names.map((name) => name.trim());
  }

  effortTrigger(mode: 'New orchestrator' | 'Edit orchestrator' = 'New orchestrator'): Locator {
    return this.locator(mode).getByRole('combobox', { name: 'Effort', exact: true });
  }

  async setEffort(
    label: string,
    mode: 'New orchestrator' | 'Edit orchestrator' = 'New orchestrator',
  ): Promise<void> {
    await this.effortTrigger(mode).click();
    await this.page.getByRole('option', { name: label, exact: true }).click();
  }

  errorAlert(mode: 'New orchestrator' | 'Edit orchestrator' = 'New orchestrator'): Locator {
    return this.locator(mode).getByRole('alert');
  }

  async setEnvRole(
    tenant: string,
    environment: string,
    role: 'Not declared' | 'Code' | 'Build',
  ): Promise<void> {
    await this.envRoleTrigger(tenant, environment).click();
    await this.page.getByRole('option', { name: role, exact: true }).click();
  }

  // Read as a list: a single-role probe cannot tell "not offered" from "the list never opened".
  async envRoleOptionNames(tenant: string, environment: string): Promise<string[]> {
    await this.envRoleTrigger(tenant, environment).click();
    const options = this.page.getByRole('option');
    await options.first().waitFor({ state: 'visible' });
    const names = await options.allInnerTexts();
    await this.page.keyboard.press('Escape');
    return names.map((name) => name.trim());
  }

  // An ineligible env is listed disabled, with an accessible name distinct from eligible rows.
  ineligibleEnvCheckbox(tenant: string, environment: string): Locator {
    return this.locator().getByRole('checkbox', {
      name: `${tenant} / ${environment} can't be linked`,
    });
  }

  envIneligibleReason(tenant: string, environment: string): Locator {
    return this.envBlock(tenant, environment).locator('p');
  }

  // A runtime env has one legal role, rendered as text instead of a Select.
  envRequiredRoleText(
    tenant: string,
    environment: string,
    mode: 'New orchestrator' | 'Edit orchestrator' = 'New orchestrator',
  ): Locator {
    return this.envBlock(tenant, environment, mode).getByText(/^Role: Runtime —/);
  }

  environmentsEmptyMessage(): Locator {
    return this.locator().getByText('No environments yet. Initialize one to orchestrate it.');
  }

  environmentsAllIneligibleMessage(): Locator {
    return this.locator().getByText(/environments? found, but none can be linked/);
  }

  async cancel(mode: 'New orchestrator' | 'Edit orchestrator' = 'New orchestrator'): Promise<void> {
    await this.locator(mode).getByRole('button', { name: 'Cancel' }).click();
  }

  nameInput(mode: 'New orchestrator' | 'Edit orchestrator' = 'New orchestrator'): Locator {
    return this.locator(mode).getByLabel('Name');
  }

  createButton(): Locator {
    return this.locator().getByRole('button', { name: 'Create' });
  }

  async create(name: string): Promise<void> {
    await this.nameInput().fill(name);
    await this.createButton().click();
  }

  saveButton(): Locator {
    return this.locator('Edit orchestrator').getByRole('button', { name: 'Save' });
  }

  async save(): Promise<void> {
    await this.saveButton().click();
  }

  // Guidance renders only in Edit mode.
  private static readonly guidanceTitle: Record<'role' | 'shared', string> = {
    role: 'Role: what this orchestrator does',
    shared: 'Shared contract: rules for every orchestrator',
  };

  guidanceLabel(layer: 'role' | 'shared'): Locator {
    return this.locator('Edit orchestrator').getByText(OrchestratorDialog.guidanceTitle[layer], {
      exact: true,
    });
  }

  guidanceRolePath(id: string): Locator {
    return this.locator('Edit orchestrator').getByText(`CLAUDE.${id}.md`, { exact: false });
  }

  guidanceSharedPath(): Locator {
    return this.locator('Edit orchestrator').getByText('CLAUDE.md', { exact: false });
  }

  guidanceOpenButton(layer: 'role' | 'shared', ide: 'VS Code' | 'IntelliJ IDEA'): Locator {
    return this.locator('Edit orchestrator').getByRole('button', {
      name: `Open ${OrchestratorDialog.guidanceTitle[layer]} in ${ide}`,
    });
  }

  restartRequiredNotice(): Locator {
    return this.locator('Edit orchestrator').getByRole('status').filter({
      hasText: 'Its environments changed while it was running',
    });
  }

  restartNowButton(): Locator {
    return this.restartRequiredNotice().getByRole('button', { name: 'Restart now' });
  }

  launchSettingsRestartNotice(): Locator {
    return this.locator('Edit orchestrator').getByRole('status').filter({
      hasText: 'Its gateway, model or effort changed while it was running',
    });
  }

  launchSettingsRestartNowButton(): Locator {
    return this.launchSettingsRestartNotice().getByRole('button', { name: 'Restart now' });
  }

  footerRestartButton(): Locator {
    return this.locator('Edit orchestrator').getByRole('button', {
      name: /^Restart( to apply)?$/,
    });
  }
}
