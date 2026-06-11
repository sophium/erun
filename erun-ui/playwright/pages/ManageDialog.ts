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

  // idleTimeoutInput targets the Runtime tab's Idle-stop "Timeout" field — a
  // pod-shaping value (helm idle.* → pod env).
  idleTimeoutInput(): Locator {
    return this.locator().locator('#environment-config-idle-timeout');
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

  // envTypeFieldValue reads the "Environment type" readonly field on the
  // General tab so specs can pick a remote env without a backend round-trip.
  // ReadonlyField puts the human label on the element with the field id and
  // labels the value sibling with aria-labelledby, so the value lives on
  // [aria-labelledby="<id>"], not on the id'd node itself.
  async envTypeFieldValue(): Promise<string> {
    const field = this.locator().locator('[aria-labelledby="environment-config-type"]');
    if (!(await field.isVisible().catch(() => false))) {
      return '';
    }
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
