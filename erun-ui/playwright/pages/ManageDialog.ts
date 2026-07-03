import type { Locator, Page } from '@playwright/test';

export type ManageTab = 'General' | 'Runtime' | 'AI' | 'Ports' | 'SSH' | 'History';

// ManageDialog POM. The dialog title is "<tenant>-<environment>".
export class ManageDialog {
  constructor(
    public readonly page: Page,
    private readonly expectedTitle?: string,
  ) {}

  locator(): Locator {
    if (this.expectedTitle) {
      return this.page.getByRole('dialog', { name: this.expectedTitle });
    }
    // Disambiguate from other open dialogs (e.g. the activity drawer) by the
    // General tab that only the manage surface has.
    return this.page
      .getByRole('dialog')
      .filter({ has: this.page.getByRole('tab', { name: /^General/ }) })
      .first();
  }

  async waitForOpen(): Promise<void> {
    await this.locator().waitFor({ state: 'visible' });
  }

  async waitForClosed(): Promise<void> {
    await this.locator().waitFor({ state: 'hidden' });
  }

  tab(name: ManageTab): Locator {
    // The tab's accessible name gains ", has unsaved changes" when dirty, so
    // match by prefix; scope to this dialog to avoid the terminal tab strip.
    return this.locator().getByRole('tab', { name: new RegExp(`^${name}(,|$)`) });
  }

  async tabHasUnsavedChanges(name: ManageTab): Promise<boolean> {
    const label = await this.tab(name).getAttribute('aria-label');
    return label?.includes('has unsaved changes') ?? false;
  }

  // The "Pending redeploy" alert appears after a save that changed a
  // pod-shaping field.
  redeployBanner(): Locator {
    return this.locator().getByRole('alert').filter({ hasText: 'Pending redeploy' });
  }

  // The "Include in Upgrade all" opt-in is selection metadata for a future
  // `erun upgrade`, never a pod input.
  autoUpgradeCheckbox(): Locator {
    return this.locator().locator('#environment-config-autoupgrade');
  }

  // The "Ignore project build.sh" toggle is a build-time CLI setting, never a
  // pod input.
  disableBuildScriptCheckbox(): Locator {
    return this.locator().locator('#environment-config-disablebuildscript');
  }

  // The Idle-stop "Timeout" is a pod-shaping value.
  idleTimeoutInput(): Locator {
    return this.locator().locator('#environment-config-idle-timeout');
  }

  // "Version to deploy": empty means deploy the current code (a builds-here
  // env orchestrates build → push → deploy); a typed version installs that
  // published version by reference.
  runtimeVersionInput(): Locator {
    return this.locator().locator('#manage-version');
  }

  async deploy(): Promise<void> {
    const button = this.locator().getByRole('button', { name: 'Deploy' });
    await button.scrollIntoViewIfNeeded();
    await button.click();
  }

  // `name` is the component's canonical name: a chart dir name, or the runtime
  // release name <tenant>-devops for the runtime item.
  deployComponentCheckbox(name: string): Locator {
    return this.locator().locator(`#environment-config-deploy-component-${name}`);
  }

  // Enabled only when the selection differs from the saved default. Matched by
  // id, not name, so it never collides with the dialog footer's Save.
  saveDeployComponentsButton(): Locator {
    return this.locator().locator('#environment-config-save-deploy-components');
  }

  async selectTab(name: ManageTab): Promise<void> {
    await this.tab(name).click();
  }

  async getActiveTab(): Promise<string> {
    const active = this.locator().locator('[role="tab"][aria-selected="true"]').first();
    const label = await active.getAttribute('aria-label');
    if (!label) return '';
    return label.split(',')[0]?.trim() || '';
  }

  async save(): Promise<void> {
    const button = this.locator().getByRole('button', { name: /^Save( |$|ing)/ });
    await button.scrollIntoViewIfNeeded();
    await button.click();
  }

  async cancel(): Promise<void> {
    const button = this.locator().getByRole('button', { name: 'Cancel' });
    await button.scrollIntoViewIfNeeded();
    await button.click();
  }

  async openDelete(): Promise<void> {
    const button = this.locator().getByRole('button', { name: /^Delete/ });
    await button.scrollIntoViewIfNeeded();
    await button.click();
  }

  async confirmDelete(expected: string): Promise<void> {
    await this.page.locator('#manage-confirmation').fill(expected);
    await this.locator()
      .getByRole('button', { name: /^Delete/ })
      .click();
  }

  // --- Container-registries editor (General tab) ---

  addRegistryButton(): Locator {
    return this.locator().getByRole('button', { name: 'Add registry' });
  }

  registryInput(index: number): Locator {
    return this.locator().locator(`#environment-config-registry-${String(index)}`);
  }

  registryRoleCheckbox(index: number, role: string): Locator {
    return this.locator().getByLabel(`${role} role for registry ${String(index + 1)}`);
  }

  removeRegistryButton(index: number): Locator {
    return this.locator().getByRole('button', { name: `Remove registry ${String(index + 1)}` });
  }

  async envTypeFieldValue(): Promise<string> {
    const field = this.environmentTypeSelect();
    if ((await field.count()) === 0) {
      return '';
    }
    await field.scrollIntoViewIfNeeded().catch(() => undefined);
    return (await field.textContent())?.trim() ?? '';
  }

  // Mirrors the Go-side EnvConfig.RemoteWorktree() predicate: true for any env
  // type other than the local-agent variant.
  async hasRemoteWorktree(): Promise<boolean> {
    const label = await this.envTypeFieldValue();
    return label !== '' && !/local agent/i.test(label);
  }

  // Only renders for remote envs (autoStart is meaningless for local shells),
  // so check visibility before driving it.
  autoStartSelect(): Locator {
    return this.locator().locator('#environment-config-autostart');
  }

  async autoStartSelectVisible(): Promise<boolean> {
    return this.autoStartSelect()
      .isVisible()
      .catch(() => false);
  }

  async autoStartSelectedValue(): Promise<string> {
    return (await this.autoStartSelect().textContent())?.trim() ?? '';
  }

  async chooseAutoStart(
    mode: 'Ask each time' | 'Always auto-start' | 'Never auto-start',
  ): Promise<void> {
    await this.autoStartSelect().click();
    // SelectField renders a Radix Select; the listbox is portal'd to body
    // so it is queried at the document root rather than inside the dialog.
    await this.page.getByRole('option', { name: mode }).click();
  }

  // Only renders when the tenant has at least one cloud alias configured (else
  // an EmptyState), so check visibility first.
  cloudAliasSelect(): Locator {
    return this.locator().locator('#environment-config-cloudprovideralias');
  }

  async cloudAliasSelectVisible(): Promise<boolean> {
    return this.cloudAliasSelect()
      .isVisible()
      .catch(() => false);
  }

  async cloudAliasSelectedValue(): Promise<string> {
    return (await this.cloudAliasSelect().textContent())?.trim() ?? '';
  }

  async openCloudAliasOptions(): Promise<void> {
    await this.cloudAliasSelect().click();
  }

  // The Radix listbox is portal'd to the document body, so it is queried at the
  // page root rather than inside the dialog.
  cloudAliasNoneOption(): Locator {
    return this.page.getByRole('option', { name: '— None —' });
  }

  async chooseCloudAliasNone(): Promise<void> {
    await this.openCloudAliasOptions();
    await this.cloudAliasNoneOption().click();
  }

  // --- Per-provider-type cloud-alias selectors ---

  cloudAliasSlotSelect(providerType: string): Locator {
    const type = providerType.trim().toLowerCase();
    const id =
      type === '' || type === 'aws'
        ? 'environment-config-cloudprovideralias'
        : `environment-config-cloudprovideralias-${type}`;
    return this.locator().locator(`#${id}`);
  }

  async cloudAliasSlotVisible(providerType: string): Promise<boolean> {
    return this.cloudAliasSlotSelect(providerType)
      .isVisible()
      .catch(() => false);
  }

  async cloudAliasSlotValue(providerType: string): Promise<string> {
    return (await this.cloudAliasSlotSelect(providerType).textContent())?.trim() ?? '';
  }

  async openCloudAliasSlotOptions(providerType: string): Promise<void> {
    await this.cloudAliasSlotSelect(providerType).click();
  }

  // The standalone "Use host AWS credentials" toggle was removed (an attached
  // AWS alias now delivers credentials); this locator asserts it never renders.
  hostAwsCredentialsCheckbox(): Locator {
    return this.locator().getByLabel('Use host AWS credentials inside this env');
  }

  // Always renders; with no per-env override it shows "Default (ultracode)".
  claudeEffortSelect(): Locator {
    return this.locator().locator('#environment-config-claude-effort');
  }

  async claudeEffortSelectedValue(): Promise<string> {
    return (await this.claudeEffortSelect().textContent())?.trim() ?? '';
  }

  // The Radix listbox is portal'd to the document body, so the option is
  // queried at the page root rather than inside the dialog.
  async chooseClaudeEffort(label: string): Promise<void> {
    await this.claudeEffortSelect().click();
    await this.page.getByRole('option', { name: label, exact: true }).click();
  }

  // An editable selector, not a read-only label, so a mis-set env type can be
  // corrected in place.
  environmentTypeSelect(): Locator {
    return this.locator().locator('#environment-config-type');
  }

  async environmentTypeSelectedValue(): Promise<string> {
    return (await this.environmentTypeSelect().textContent())?.trim() ?? '';
  }

  // The Radix listbox is portal'd to the document body, so the option is
  // queried at the page root rather than inside the dialog.
  async chooseEnvironmentType(label: string): Promise<void> {
    await this.environmentTypeSelect().click();
    await this.page.getByRole('option', { name: label, exact: true }).click();
  }

  // The "Repository path" field is an editable Input for a local-agent env but
  // a read-only label for remote-agent/runtime envs.
  repositoryPathInput(): Locator {
    // Tag-qualified because the read-only variant reuses this id on a <div>;
    // only the editable local-agent field is an <input>.
    return this.locator().locator('input#environment-config-repopath');
  }

  repositoryPathValue(): Promise<string> {
    return this.repositoryPathInput().inputValue();
  }

  async setRepositoryPath(value: string): Promise<void> {
    await this.repositoryPathInput().fill(value);
  }

  repositoryPathReadonlyValue(): Locator {
    return this.locator().locator('[aria-labelledby="environment-config-repopath"]');
  }

  claudeModelCheckbox(model: string): Locator {
    return this.locator().locator(`#environment-config-claude-models-${model}`);
  }

  // Always renders; with no per-env override it shows "Default (<first
  // available model>)", e.g. "Default (opus)".
  claudeDefaultModelSelect(): Locator {
    return this.locator().locator('#environment-config-claude-default-model');
  }

  async claudeDefaultModelSelectedValue(): Promise<string> {
    return (await this.claudeDefaultModelSelect().textContent())?.trim() ?? '';
  }

  // The Radix listbox is portal'd to the document body, so the option is
  // queried at the page root.
  async chooseClaudeDefaultModel(label: string): Promise<void> {
    await this.claudeDefaultModelSelect().click();
    await this.page.getByRole('option', { name: label, exact: true }).click();
  }

  claudeVerboseDebugCheckbox(): Locator {
    return this.locator().locator('#environment-config-claude-verbose-debug');
  }
}
