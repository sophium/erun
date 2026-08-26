import type { Locator, Page } from '@playwright/test';

export type ManageTab = 'General' | 'Runtime' | 'AI' | 'Ports' | 'Access' | 'History';

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
    // description text unique to the manage surface. The General tab used to
    // serve this purpose, but it — along with every other tab — is replaced
    // by the delete-confirmation view while dialog.tab === 'delete', which
    // made every locator scoped under this one resolve to nothing mid-delete.
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

  // The "Platform account" toggle binds the env's runtime SA to cluster-admin;
  // env-type agnostic, so it renders for every environment type.
  platformAccountCheckbox(): Locator {
    return this.locator().locator('#environment-config-platformaccount');
  }

  // The runtime-only "Mount source code" toggle and the git remote it reveals.
  // The URL field is rendered only while the toggle is on.
  mountSourceCheckbox(): Locator {
    return this.locator().locator('#environment-config-mountsource');
  }

  repoURLInput(): Locator {
    return this.locator().locator('input#environment-config-repourl');
  }

  // The Idle-stop "Timeout" is a pod-shaping value.
  idleTimeoutInput(): Locator {
    return this.locator().locator('#environment-config-idle-timeout');
  }

  // "Version to deploy": the version the Deploy button installs by reference
  // (empty = the env's current version). Deploy never builds — producing a new
  // version is the separate "Create & deploy new version" action.
  runtimeVersionInput(): Locator {
    return this.locator().locator('#manage-version');
  }

  // The version + components live in one popover: the chevron beside the version
  // field opens the version list and, below it, that version's component
  // checklist. Idempotent — a no-op when the panel is already open.
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

  // Picks a version from the picker's suggestion list, which keeps the panel
  // open so the version-scoped component checklist is reachable in one flow.
  async pickVersion(version: string): Promise<void> {
    await this.openVersionPicker();
    const escaped = version.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
    await this.page.getByRole('option', { name: new RegExp(escaped) }).click();
  }

  // Deploy installs the selected version by reference (never builds), so it is
  // disabled until a version is chosen.
  deployButton(): Locator {
    return this.locator().locator('#environment-config-deploy');
  }

  // "Stop environment" scales the runtime to zero so its CPU and memory go back
  // to the node. It sits beside Deploy because that is where the operator finds
  // the resource sliders capped. There is no matching Start: opening the
  // environment wakes it.
  stopButton(): Locator {
    return this.locator().locator('#environment-config-stop');
  }

  stopHelperText(): Locator {
    return this.locator().locator('#environment-config-stop-help');
  }

  // "This environment's usage" reports the environment's own CPU, memory and
  // disk reading against its cgroup limits, directly below the sliders that
  // set those limits.
  runtimeUsagePanel(): Locator {
    return this.locator()
      .locator('div')
      .filter({ hasText: /^This environment's usage/ })
      .first();
  }

  runtimeUsageRefreshButton(): Locator {
    return this.locator().locator('#environment-config-usage-refresh');
  }

  // "Running in this environment" reports what the pod is actually running —
  // observed sessions and the processes holding memory — beneath the sliders,
  // because that is the next question once the figures read as capped.
  runtimeActivityPanel(): Locator {
    return this.locator()
      .locator('div')
      .filter({ hasText: /^Running in this environment/ })
      .first();
  }

  runtimeActivityRefreshButton(): Locator {
    return this.locator().locator('#environment-config-activity-refresh');
  }

  // A reclaim button exists only for a group with a safe reclaim action; an
  // agent process is the operator's work and deliberately has none.
  runtimeReclaimButton(group: string): Locator {
    return this.locator().locator(`#environment-config-reclaim-${group}`);
  }

  // The capacity reading's explanation line: why the maximum is what it is.
  runtimeCapacityNotice(): Locator {
    return this.locator()
      .getByRole('status')
      .filter({ hasText: /node is fully committed|declare no limits/ });
  }

  // "Create & deploy new version" (build → push → deploy) — shown only for a
  // local-agent env, which owns source to build.
  createVersionButton(): Locator {
    return this.locator().locator('#environment-config-create-version');
  }

  async deploy(): Promise<void> {
    const button = this.deployButton();
    await button.scrollIntoViewIfNeeded();
    await button.click();
  }

  // The checklist lives in the version-picker popover, which Radix portals to
  // the document root — so these query at page level, not inside the dialog.
  // `name` is the component's canonical name: a chart dir name, or the runtime
  // release name <tenant>-devops for the runtime item.
  deployComponentCheckbox(name: string): Locator {
    return this.page.locator(`#environment-config-deploy-component-${name}`);
  }

  // Version-scoped ("Components in <version> to deploy") only for sourceless
  // envs; a local env's charts aren't version-filtered, so it stays plain.
  deployComponentsHeading(): Locator {
    return this.page.locator('#environment-config-deploy-components-heading');
  }

  // Source-failure notices below the version list (a private or unreachable
  // runtime image). Portaled with the popover, so query at page level.
  versionSourceNotices(): Locator {
    return this.page.getByRole('list', { name: 'Version source notices' });
  }

  // The version-picker popover itself (portaled to the document root), used to
  // assert it stays within the viewport rather than overflowing off-screen.
  versionPickerPopover(): Locator {
    return this.page.locator('[data-slot="popover-content"]');
  }

  // Shown in place of the checklist until a version is picked — the charts are
  // that version's, so there's nothing to choose before one is selected.
  deployComponentsHint(): Locator {
    return this.page.locator('#environment-config-deploy-components-hint');
  }

  // Enabled only when the selection differs from the saved default. Matched by
  // id, not name, so it never collides with the dialog footer's Save.
  saveDeployComponentsButton(): Locator {
    return this.page.locator('#environment-config-save-deploy-components');
  }

  // The "Runtime chart" field states the chart coordinate -- which chart the
  // runtime is installed from -- separately from the version, which names the
  // image. Empty means "the chart published with the deployed version".
  runtimeChartInput(): Locator {
    return this.locator().locator('#environment-config-runtimechart');
  }

  // The notice under the version row: what a deploy of the picked version would
  // install for the runtime, or why it cannot be deployed as it stands.
  // Page-scoped, not dialog-scoped: while the version panel is open it is a modal
  // popover and the dialog behind it is aria-hidden, so a role-scoped query would
  // find nothing exactly when this notice matters most.
  runtimeChartNotice(): Locator {
    return this.page.locator('#environment-config-runtimechart-notice');
  }

  // The same statement rendered inside the open version panel.
  runtimeChartPanelNotice(): Locator {
    return this.page.locator('#environment-config-runtimechart-notice-panel');
  }

  // One-click recovery offered by the blocking notice: adopt an ERun chart. Each
  // notice owns its own button id, so the row and the panel never collide.
  adoptRuntimeChartButton(): Locator {
    return this.page.locator('#environment-config-runtimechart-notice-adopt');
  }

  adoptRuntimeChartButtonInPanel(): Locator {
    return this.page.locator('#environment-config-runtimechart-notice-panel-adopt');
  }

  async openRuntimeChartPicker(): Promise<void> {
    await this.locator().getByRole('button', { name: 'Show runtime chart choices' }).click();
  }

  // Picks an offered chart. The options carry both the label and the reference,
  // so either matches.
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

  jobRows(): Locator {
    return this.locator().getByTestId('manage-jobs-row');
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
