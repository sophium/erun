import type { Locator, Page } from '@playwright/test';

export type ManageTab = 'General' | 'Runtime' | 'AI' | 'Ports' | 'Access' | 'History';

export class ManageDialog {
  constructor(
    public readonly page: Page,
    private readonly expectedTitle?: string,
  ) {}

  locator(): Locator {
    if (this.expectedTitle) {
      return this.page.getByRole('dialog', { name: this.expectedTitle });
    }
    // Matched by the manage description, which survives the delete-confirmation view.
    return this.page
      .getByRole('dialog')
      .filter({ hasText: 'Edit environment configuration' })
      .first();
  }

  async waitForOpen(): Promise<void> {
    await this.locator().waitFor({ state: 'visible' });
  }

  async waitForClosed(): Promise<void> {
    await this.locator().waitFor({ state: 'hidden' });
  }

  tab(name: ManageTab): Locator {
    // Prefix match: the name gains ", has unsaved changes" when dirty.
    return this.locator().getByRole('tab', { name: new RegExp(`^${name}(,|$)`) });
  }

  async tabHasUnsavedChanges(name: ManageTab): Promise<boolean> {
    const label = await this.tab(name).getAttribute('aria-label');
    return label?.includes('has unsaved changes') ?? false;
  }

  redeployBanner(): Locator {
    return this.locator().getByRole('alert').filter({ hasText: 'Pending redeploy' });
  }

  // Shares the Runtime checklist, so an emptied checklist refuses it too.
  redeployNowButton(): Locator {
    return this.redeployBanner().getByRole('button', { name: 'Redeploy now' });
  }

  // Waits under the test's own budget, not expect's fixed timeout.
  async waitForRedeployBanner(): Promise<void> {
    await this.redeployBanner().waitFor({ state: 'visible' });
  }

  autoUpgradeCheckbox(): Locator {
    return this.locator().locator('#environment-config-autoupgrade');
  }

  disableBuildScriptCheckbox(): Locator {
    return this.locator().locator('#environment-config-disablebuildscript');
  }

  // Renders for every pod-backed type; a host env has no runtime SA.
  platformAccountCheckbox(): Locator {
    return this.locator().locator('#environment-config-platformaccount');
  }

  // Hosted environment panel, scoped to its named region: the dialog has other role=status
  // elements. Its two status lines are told apart by accessible name.
  hostedPanel(): Locator {
    return this.locator().getByRole('region', { name: 'Hosted environment' });
  }

  hostedCheckButton(): Locator {
    return this.hostedPanel().getByRole('button', { name: /Check whether the platform's copy/ });
  }

  hostedLocalChangeLine(): Locator {
    return this.hostedPanel().getByRole('status', { name: "This machine's settings" });
  }

  hostedUploadButton(): Locator {
    return this.hostedPanel().getByRole('button', {
      name: "Upload this environment's settings to the platform",
    });
  }

  hostedDriftLine(): Locator {
    return this.hostedPanel().getByRole('status', { name: "The platform's definition" });
  }

  // Failures are alerts, not statuses.
  hostedDriftError(): Locator {
    return this.hostedPanel().getByRole('alert');
  }

  // The URL field renders only while the toggle is on.
  mountSourceCheckbox(): Locator {
    return this.locator().locator('#environment-config-mountsource');
  }

  repoURLInput(): Locator {
    return this.locator().locator('input#environment-config-repourl');
  }

  idleTimeoutInput(): Locator {
    return this.locator().locator('#environment-config-idle-timeout');
  }

  // Deploy installs by reference; it never builds.
  runtimeVersionInput(): Locator {
    return this.locator().locator('#manage-version');
  }

  // Opens the version list and component checklist; a no-op if already open.
  async openVersionPicker(): Promise<void> {
    if (
      await this.deployComponentsHeading()
        .isVisible()
        .catch(() => false)
    ) {
      return;
    }
    await this.locator().getByRole('button', { name: 'Show version choices' }).click();
    await this.deployComponentsHeading().waitFor({ state: 'visible' });
  }

  // Keeps the panel open so the checklist is reachable.
  async pickVersion(version: string): Promise<void> {
    await this.openVersionPicker();
    const escaped = version.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
    await this.page.getByRole('option', { name: new RegExp(escaped) }).click();
  }

  deployButton(): Locator {
    return this.locator().locator('#environment-config-deploy');
  }

  // There is no matching Start: opening the environment wakes it.
  stopButton(): Locator {
    return this.locator().locator('#environment-config-stop');
  }

  runtimeRunStateLine(): Locator {
    return this.locator().locator('#environment-config-run-state');
  }

  stopHelperText(): Locator {
    return this.locator().locator('#environment-config-stop-help');
  }

  runtimeUsagePanel(): Locator {
    return this.locator()
      .locator('div')
      .filter({ hasText: /^This environment's usage/ })
      .first();
  }

  runtimeUsageRefreshButton(): Locator {
    return this.locator().locator('#environment-config-usage-refresh');
  }

  runtimeSizingPanel(): Locator {
    return this.locator()
      .locator('div')
      .filter({ hasText: /^Sizing recommendation/ })
      .first();
  }

  runtimeSizingRefreshButton(): Locator {
    return this.locator().locator('#environment-config-sizing-refresh');
  }

  runtimeSizingApplyButton(): Locator {
    return this.locator().locator('#environment-config-sizing-apply');
  }

  // The override is a deliberate second click.
  runtimeSizingOverrideButton(): Locator {
    return this.locator().locator('#environment-config-sizing-override');
  }

  runtimeActivityPanel(): Locator {
    return this.locator()
      .locator('div')
      .filter({ hasText: /^Running in this environment/ })
      .first();
  }

  runtimeActivityRefreshButton(): Locator {
    return this.locator().locator('#environment-config-activity-refresh');
  }

  // Agent processes deliberately have no reclaim action.
  runtimeReclaimButton(group: string): Locator {
    return this.locator().locator(`#environment-config-reclaim-${group}`);
  }

  runtimeCapacityNotice(): Locator {
    return this.locator()
      .getByRole('status')
      .filter({ hasText: /node is fully committed|declare no limits/ });
  }

  // Only for local-agent envs, which own source to build.
  createVersionButton(): Locator {
    return this.locator().locator('#environment-config-create-version');
  }

  async deploy(): Promise<void> {
    const button = this.deployButton();
    await button.scrollIntoViewIfNeeded();
    await button.click();
  }

  // The checklist is portaled to the document root. `name` is a chart dir or <tenant>-devops.
  deployComponentCheckbox(name: string): Locator {
    return this.page.locator(`#environment-config-deploy-component-${name}`);
  }

  // Version-scoped only for sourceless envs.
  deployComponentsHeading(): Locator {
    return this.page.locator('#environment-config-deploy-components-heading');
  }

  // Portaled with the popover.
  versionSourceNotices(): Locator {
    return this.page.getByRole('list', { name: 'Version source notices' });
  }

  versionPickerPopover(): Locator {
    return this.page.locator('[data-slot="popover-content"]');
  }

  versionPickerSearchInput(): Locator {
    return this.page.getByPlaceholder('Search versions...');
  }

  deployComponentsHint(): Locator {
    return this.page.locator('#environment-config-deploy-components-hint');
  }

  // Matched by id so it never collides with the footer's Save.
  saveDeployComponentsButton(): Locator {
    return this.page.locator('#environment-config-save-deploy-components');
  }

  deployComponentsEmptyNotice(): Locator {
    return this.page.locator('#environment-config-deploy-components-empty-notice');
  }

  deployComponentsEmptyPanelNotice(): Locator {
    return this.page.locator('#environment-config-deploy-components-empty-notice-panel');
  }

  // Empty means the chart published with the deployed version.
  runtimeChartInput(): Locator {
    return this.locator().locator('#environment-config-runtimechart');
  }

  // Page-scoped: while the version panel is open the dialog is aria-hidden.
  runtimeChartNotice(): Locator {
    return this.page.locator('#environment-config-runtimechart-notice');
  }

  runtimeChartPanelNotice(): Locator {
    return this.page.locator('#environment-config-runtimechart-notice-panel');
  }

  // Each notice owns its button id, so row and panel never collide.
  adoptRuntimeChartButton(): Locator {
    return this.page.locator('#environment-config-runtimechart-notice-adopt');
  }

  adoptRuntimeChartButtonInPanel(): Locator {
    return this.page.locator('#environment-config-runtimechart-notice-panel-adopt');
  }

  // Waits for the popover to open; the paired-default option always renders first.
  async openRuntimeChartPicker(): Promise<void> {
    await this.locator().getByRole('button', { name: 'Show runtime chart choices' }).click();
    await this.page
      .getByRole('option', { name: /Published with the deployed version/ })
      .waitFor({ state: 'visible' });
  }

  // Options carry both label and reference, so either matches.
  async pickRuntimeChart(text: string): Promise<void> {
    const escaped = text.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
    await this.page.getByRole('option', { name: new RegExp(escaped) }).click();
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
    const button = this.locator().getByRole('button', { name: 'Cancel', exact: true });
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
      .getByRole('button', { name: /^Confirm delete/ })
      .click();
  }

  // --- Container-registries editor (General tab) ---

  // --- Jobs tab ---

  jobsTabTrigger(): Locator {
    return this.locator().getByRole('tab', { name: 'Jobs' });
  }

  jobsEmptyState(): Locator {
    return this.locator().getByTestId('manage-jobs-empty');
  }

  // The reading placeholder has its own id so an empty-state wait cannot settle on it.
  jobsLoadingState(): Locator {
    return this.locator().getByTestId('manage-jobs-loading');
  }

  jobsUnreachable(): Locator {
    return this.locator().getByTestId('manage-jobs-unreachable');
  }

  jobsUnreachableReconnectButton(): Locator {
    return this.locator().getByTestId('manage-jobs-unreachable-reconnect');
  }

  // A refused read (timeout, auth, parse), distinct from the unreachable card.
  jobsReadFailure(): Locator {
    return this.locator()
      .getByRole('alert')
      .filter({ hasText: "Could not read this environment's jobs" });
  }

  jobsReadFailureRetry(): Locator {
    return this.locator().getByTestId('manage-jobs-retry');
  }

  jobRows(): Locator {
    return this.locator().getByTestId('manage-jobs-row');
  }

  jobRowName(index: number): Locator {
    return this.locator().getByTestId('manage-jobs-row-name').nth(index);
  }

  jobRowDetail(index: number): Locator {
    return this.locator().getByTestId('manage-jobs-row-detail').nth(index);
  }

  jobOutcome(index: number): Locator {
    return this.locator().getByTestId('manage-jobs-row-outcome').nth(index);
  }

  jobShowOutputButton(name: string): Locator {
    return this.locator().getByRole('button', { name: `Show output for ${name}` });
  }

  jobOutput(): Locator {
    return this.locator().getByTestId('manage-jobs-output');
  }

  jobOutputEmpty(): Locator {
    return this.locator().getByTestId('manage-jobs-output-empty');
  }

  jobCancelButton(name: string): Locator {
    return this.locator().getByRole('button', { name: `Cancel job ${name}` });
  }

  jobConfirmCancelButton(name: string): Locator {
    return this.locator().getByRole('button', { name: `Confirm cancelling ${name}` });
  }

  addPullSecretButton(): Locator {
    return this.locator().getByRole('button', { name: 'Add image pull secret' });
  }

  pullSecretInput(index: number): Locator {
    return this.locator().getByRole('textbox', { name: `Image pull secret ${String(index + 1)}` });
  }

  removePullSecretButton(index: number): Locator {
    return this.locator().getByRole('button', {
      name: `Remove image pull secret ${String(index + 1)}`,
    });
  }

  runtimeRegistryInput(): Locator {
    return this.locator().getByLabel('Runtime registry');
  }

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

  // Mirrors EnvConfig.RemoteWorktree(). Throws while the body is still loading rather than
  // reading an absent field as "not remote".
  async hasRemoteWorktree(): Promise<boolean> {
    const label = await this.envTypeFieldValue();
    if (label === '') {
      throw new Error(
        'The Environment type field is not readable (the manage dialog body is still loading); refusing to answer whether this environment has a remote worktree.',
      );
    }
    return !/local agent/i.test(label);
  }

  // Remote envs only.
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
    // The listbox is portaled to body.
    await this.page.getByRole('option', { name: mode }).click();
  }

  // Renders only when the tenant has a cloud alias.
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

  // The listbox is portaled to body.
  cloudAliasNoneOption(): Locator {
    return this.page.getByRole('option', { name: '— None —' });
  }

  async chooseCloudAliasNone(): Promise<void> {
    await this.openCloudAliasOptions();
    await this.cloudAliasNoneOption().click();
  }

  async chooseCloudAlias(alias: string): Promise<void> {
    await this.openCloudAliasOptions();
    await this.page.getByRole('option', { name: alias, exact: true }).click();
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

  // Removed control; asserts it never renders.
  hostAwsCredentialsCheckbox(): Locator {
    return this.locator().getByLabel('Use host AWS credentials inside this env');
  }

  // --- Per-environment gateway overrides ---
  // Rendered only when the erun-level catalog names a gateway.

  claudeGatewayField(): Locator {
    return this.locator().locator('#environment-config-claude-gateway');
  }

  // The listbox is portaled to body.
  async chooseClaudeGateway(label: string): Promise<void> {
    await this.claudeGatewayField().click();
    await this.page.getByRole('option', { name: label, exact: true }).click();
  }

  // Removed control; asserts its absence.
  claudeGatewaySecretInput(): Locator {
    return this.locator().locator('#environment-config-claude-gateway-secret');
  }

  claudeEffortSelect(): Locator {
    return this.locator().locator('#environment-config-claude-effort');
  }

  async claudeEffortSelectedValue(): Promise<string> {
    return (await this.claudeEffortSelect().textContent())?.trim() ?? '';
  }

  // The listbox is portaled to body.
  async chooseClaudeEffort(label: string): Promise<void> {
    await this.claudeEffortSelect().click();
    await this.page.getByRole('option', { name: label, exact: true }).click();
  }

  environmentTypeSelect(): Locator {
    return this.locator().locator('#environment-config-type');
  }

  async environmentTypeSelectedValue(): Promise<string> {
    return (await this.environmentTypeSelect().textContent())?.trim() ?? '';
  }

  // The listbox is portaled to body.
  async chooseEnvironmentType(label: string): Promise<void> {
    await this.environmentTypeSelect().click();
    await this.page.getByRole('option', { name: label, exact: true }).click();
  }

  repositoryPathInput(): Locator {
    // Tag-qualified: the read-only variant reuses this id on a <div>.
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

  claudeDefaultModelSelect(): Locator {
    return this.locator().locator('#environment-config-claude-default-model');
  }

  async claudeDefaultModelSelectedValue(): Promise<string> {
    return (await this.claudeDefaultModelSelect().textContent())?.trim() ?? '';
  }

  // The listbox is portaled to body.
  async chooseClaudeDefaultModel(label: string): Promise<void> {
    await this.claudeDefaultModelSelect().click();
    await this.page.getByRole('option', { name: label, exact: true }).click();
  }

  claudeVerboseDebugCheckbox(): Locator {
    return this.locator().locator('#environment-config-claude-verbose-debug');
  }
}
