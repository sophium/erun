import type { Locator, Page } from '@playwright/test';

export type ManageTab = 'General' | 'Runtime' | 'AI' | 'Ports' | 'SSH' | 'History';

// ManageDialog POM. The dialog title is "<tenant>-<environment>", so the
// caller may pass the expected title for a strict match; otherwise the POM
// finds any open dialog under that pattern.
export class ManageDialog {
  constructor(
    public readonly page: Page,
    private readonly expectedTitle?: string,
  ) {}

  locator(): Locator {
    if (this.expectedTitle) {
      return this.page.getByRole('dialog', { name: this.expectedTitle });
    }
    // The manage dialog title is "<tenant>-<environment>"; pick the
    // open dialog that contains the Save+Cancel footer pair plus a
    // visible General tab — that's the manage surface and not, for
    // example, the activity drawer.
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
    // TabsTrigger renders with role=tab and an aria-label that includes the
    // tab name (plus ", has unsaved changes" when dirty). Match by partial
    // accessible name so dirty state still resolves. Scope to the manage
    // dialog so we don't collide with the terminal tab strip's tablist.
    return this.locator().getByRole('tab', { name: new RegExp(`^${name}(,|$)`) });
  }

  // tabHasUnsavedChanges reads the dirty marker off the tab trigger's
  // accessible name ("<label>, has unsaved changes" — issue #460).
  async tabHasUnsavedChanges(name: ManageTab): Promise<boolean> {
    const label = await this.tab(name).getAttribute('aria-label');
    return label?.includes('has unsaved changes') ?? false;
  }

  // tabHasWarning reads the warning marker off a tab trigger's accessible name
  // ("<label>, has a warning" — issue #641 runtime-capacity feedback).
  async tabHasWarning(name: ManageTab): Promise<boolean> {
    const label = await this.tab(name).getAttribute('aria-label');
    return label?.includes('has a warning') ?? false;
  }

  // saveButton targets the footer Save action (to assert enabled/disabled).
  saveButton(): Locator {
    return this.locator().getByRole('button', { name: /^Save( |$|ing)/ });
  }

  // saveStatus targets the footer region that explains why Save is disabled, or
  // warns that a deploy is at risk, right where the user acts (issue #641).
  saveStatus(): Locator {
    return this.locator().locator('#manage-save-status');
  }

  // goToRuntimeButton targets the recovery action inside the save-status region.
  goToRuntimeButton(): Locator {
    return this.saveStatus().getByRole('button', { name: 'Go to Runtime' });
  }

  // redeployBanner targets the amber "Pending redeploy" alert raised after a
  // save that changed a pod-shaping field (issue #460).
  redeployBanner(): Locator {
    return this.locator().getByRole('alert').filter({ hasText: 'Pending redeploy' });
  }

  // autoUpgradeCheckbox targets the Runtime tab's "Include in Upgrade all"
  // opt-in — selection metadata for a future `erun upgrade`, never a pod
  // input (issue #460).
  autoUpgradeCheckbox(): Locator {
    return this.locator().locator('#environment-config-autoupgrade');
  }

  // disableBuildScriptCheckbox targets the Runtime tab's "Ignore project
  // build.sh" toggle (EnvConfig.disableBuildScript, issue #533) — a build-time
  // CLI setting, never a pod input.
  disableBuildScriptCheckbox(): Locator {
    return this.locator().locator('#environment-config-disablebuildscript');
  }

  // idleTimeoutInput targets the Runtime tab's Idle-stop "Timeout" field — a
  // pod-shaping value (helm idle.* → pod env).
  idleTimeoutInput(): Locator {
    return this.locator().locator('#environment-config-idle-timeout');
  }

  // runtimeVersionInput targets the Runtime tab's "Version to deploy" field
  // (RuntimeDeployVersionPicker). Empty means "deploy the current code": for a
  // builds-here agent env the desktop orchestrates build -> push -> deploy from
  // it; a typed version installs that published version by reference (#558).
  runtimeVersionInput(): Locator {
    return this.locator().locator('#manage-version');
  }

  // deploy clicks the Runtime tab's "Deploy" action (the Rocket button) that
  // submits submitManageDeploy with the current version field.
  async deploy(): Promise<void> {
    const button = this.locator().getByRole('button', { name: 'Deploy' });
    await button.scrollIntoViewIfNeeded();
    await button.click();
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

  // --- Container-registries editor (General tab, issue #527) ---

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

  // envTypeFieldValue reads the "Environment type" field on the General tab so
  // specs can read the env's resolved type without a backend round-trip.
  //
  // The type field used to be a ReadonlyField (value carried on a sibling
  // labelled [aria-labelledby="environment-config-type"]), but #617 turned it
  // into the correctable EnvironmentTypeField SelectField whose value text
  // lives on the trigger itself (id="environment-config-type"). The old
  // aria-labelledby selector matches nothing now, which silently read every
  // type as "" — caught here and fixed alongside the #630 cloud-alias work.
  // Read the SelectField trigger's text content (its selected-value label).
  async envTypeFieldValue(): Promise<string> {
    const field = this.environmentTypeSelect();
    if ((await field.count()) === 0) {
      return '';
    }
    await field.scrollIntoViewIfNeeded().catch(() => undefined);
    return (await field.textContent())?.trim() ?? '';
  }

  // hasRemoteWorktree returns true when the env type is anything other than
  // the local-agent variant. Mirrors the Go-side EnvConfig.RemoteWorktree()
  // predicate by reading the Environment type label rendered on the
  // General tab — the canonical control after #375 collapsed the legacy
  // remote/snapshot pair into Type.
  async hasRemoteWorktree(): Promise<boolean> {
    const label = await this.envTypeFieldValue();
    return label !== '' && !/local agent/i.test(label);
  }

  // autoStartSelect targets the "Auto-start when opening" SelectField on the
  // General tab. The select only renders for remote envs (autoStart is
  // desktop-only and meaningless for local shells), so specs should check
  // visibility before driving it.
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

  // cloudAliasSelect targets the "Cloud alias" SelectField on the General tab.
  // It only renders when the tenant has at least one cloud alias configured;
  // otherwise an EmptyState renders instead, so specs check visibility first.
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

  // cloudAliasNoneOption targets the "— None —" clear entry (issue #211). The
  // Radix listbox is portal'd to the document body, so it is queried at the
  // page root rather than inside the dialog.
  cloudAliasNoneOption(): Locator {
    return this.page.getByRole('option', { name: '— None —' });
  }

  async chooseCloudAliasNone(): Promise<void> {
    await this.openCloudAliasOptions();
    await this.cloudAliasNoneOption().click();
  }

  // --- Per-provider-type cloud-alias selectors (issue #630) ---

  // cloudAliasSlotSelect targets the per-provider-type cloud-alias selector on
  // the General tab. The AWS slot keeps the historical id
  // (#environment-config-cloudprovideralias); every other provider type
  // ("cloudflare") uses a suffixed id, so an env attaching both aliases renders
  // two addressable selectors.
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

  // hostAwsCredentialsCheckbox targets the removed standalone "Use host AWS
  // credentials" toggle. It must not render — attaching an AWS alias now
  // delivers its credentials into the env (issue #641), so there is no separate
  // toggle to reconcile against the alias selectors.
  hostAwsCredentialsCheckbox(): Locator {
    return this.locator().getByLabel('Use host AWS credentials inside this env');
  }

  // claudeEffortSelect targets the "Effort" SelectField in the Claude section
  // of the AI tab (issues #469/#491). It always renders; with no per-env
  // override it shows "Default (ultracode)".
  claudeEffortSelect(): Locator {
    return this.locator().locator('#environment-config-claude-effort');
  }

  async claudeEffortSelectedValue(): Promise<string> {
    return (await this.claudeEffortSelect().textContent())?.trim() ?? '';
  }

  // chooseClaudeEffort opens the Effort listbox and picks an option by its
  // visible label. The Radix listbox is portal'd to the document body, so the
  // option is queried at the page root rather than inside the dialog.
  async chooseClaudeEffort(label: string): Promise<void> {
    await this.claudeEffortSelect().click();
    await this.page.getByRole('option', { name: label, exact: true }).click();
  }

  // environmentTypeSelect targets the "Environment type" SelectField on the
  // General tab (issue #615). It is an editable selector — not a read-only
  // label — so a mis-set type can be corrected in place; the value shows the
  // env's type label.
  environmentTypeSelect(): Locator {
    return this.locator().locator('#environment-config-type');
  }

  async environmentTypeSelectedValue(): Promise<string> {
    return (await this.environmentTypeSelect().textContent())?.trim() ?? '';
  }

  // chooseEnvironmentType opens the type listbox and picks an option by its
  // visible label. The Radix listbox is portal'd to the document body, so the
  // option is queried at the page root rather than inside the dialog.
  async chooseEnvironmentType(label: string): Promise<void> {
    await this.environmentTypeSelect().click();
    await this.page.getByRole('option', { name: label, exact: true }).click();
  }

  // claudeModelCheckbox targets one "Available models" checkbox in the Claude
  // section of the AI tab. The id suffix is the model token itself.
  claudeModelCheckbox(model: string): Locator {
    return this.locator().locator(`#environment-config-claude-models-${model}`);
  }

  // claudeDefaultModelSelect targets the "Default model" SelectField (issue
  // #482). It always renders; with no per-env override it shows
  // "Default (Claude decides)".
  claudeDefaultModelSelect(): Locator {
    return this.locator().locator('#environment-config-claude-default-model');
  }

  async claudeDefaultModelSelectedValue(): Promise<string> {
    return (await this.claudeDefaultModelSelect().textContent())?.trim() ?? '';
  }

  // chooseClaudeDefaultModel opens the Default model listbox and picks an
  // option by its visible label. The Radix listbox is portal'd to the
  // document body, so the option is queried at the page root.
  async chooseClaudeDefaultModel(label: string): Promise<void> {
    await this.claudeDefaultModelSelect().click();
    await this.page.getByRole('option', { name: label, exact: true }).click();
  }

  // claudeVerboseDebugCheckbox targets the "Launch Claude in verbose + debug
  // mode" launch toggle (issue #477).
  claudeVerboseDebugCheckbox(): Locator {
    return this.locator().locator('#environment-config-claude-verbose-debug');
  }
}
