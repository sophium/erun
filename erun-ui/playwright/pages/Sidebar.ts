import { expect, type Locator, type Page } from '@playwright/test';

export class Sidebar {
  constructor(public readonly page: Page) {}

  locator(): Locator {
    return this.page.locator('aside').first();
  }

  async openSettings(): Promise<void> {
    await this.page.getByRole('button', { name: 'Open ERun settings' }).click();
  }

  async openUpgradeAll(): Promise<void> {
    await this.page.getByRole('button', { name: 'Upgrade all environments' }).click();
  }

  upgradeAllDialog(): Locator {
    return this.page.getByRole('dialog', { name: 'Upgrade all environments' });
  }

  async openInitDialog(): Promise<void> {
    const button = this.page.getByRole('button', { name: 'Initialize new remote environment' });
    if (await button.isVisible().catch(() => false)) {
      await button.click();
      return;
    }
    // Empty state fallback: when no tenants exist yet, the sidebar shows an
    // inline "Initialize environment" button instead of the icon trigger.
    await this.page.getByRole('button', { name: 'Initialize environment' }).click();
  }

  tenantRow(name: string): Locator {
    return this.page.locator(
      `button[aria-label="Collapse ${name}"], button[aria-label="Expand ${name}"]`,
    );
  }

  async toggleTenant(name: string): Promise<void> {
    await this.tenantRow(name).first().click();
  }

  async openTenantDashboard(name: string): Promise<void> {
    await this.page.getByRole('button', { name: `Open ${name} dashboard` }).click();
  }

  async isTenantExpanded(name: string): Promise<boolean> {
    const value = await this.tenantRow(name).first().getAttribute('aria-expanded');
    return value === 'true';
  }

  environmentRow(tenant: string, env: string): Locator {
    // The edit button next to each env row encodes both names in its
    // aria-label, which uniquely identifies the row even when env names
    // repeat across tenants ("local" lives in multiple tenants).
    return this.page.getByRole('button', { name: `Edit ${tenant} / ${env} settings` });
  }

  async openEnvironment(tenant: string, env: string): Promise<void> {
    // Prefix-match the label: local envs append a "(local)" suffix, so an exact match would miss them.
    await this.page.locator(`button[aria-label^="${tenant} / ${env}"]`).first().click();
  }

  async openManageDialogFor(tenant: string, env: string): Promise<void> {
    await this.environmentRow(tenant, env).click();
  }

  // The Outputs button stops propagation, so opening outputs never also opens the env itself.
  async openOutputs(tenant: string, env: string): Promise<void> {
    await this.hoverEnvironmentRow(tenant, env);
    await this.page.getByRole('button', { name: `Outputs for ${tenant} / ${env}` }).click();
  }

  // Drive the edit button by keyboard, not mouse: a hover opens the row's
  // IconTooltip whose popper would intercept the click. Focusing the row makes
  // the pointer-events-none button interactive; Enter fires it without a hover.
  //
  // Retry the Enter: a boot-reattached env restores its terminal, which steals
  // focus asynchronously and can swallow the keydown before the dialog opens.
  async openManageDialogViaKeyboard(tenant: string, env: string): Promise<void> {
    const dialog = this.page
      .getByRole('dialog')
      .filter({ has: this.page.getByRole('tab', { name: /^General/ }) })
      .first();
    for (let attempt = 0; attempt < 4; attempt++) {
      await this.environmentRow(tenant, env).press('Enter');
      const opened = await dialog.waitFor({ state: 'visible', timeout: 2_000 }).then(
        () => true,
        () => false,
      );
      if (opened) {
        return;
      }
    }
    throw new Error(`manage dialog did not open for ${tenant} / ${env} via keyboard`);
  }

  // Targets the clickable env-row button, not the edit button environmentRow() returns.
  envRowButton(tenant: string, env: string): Locator {
    return this.page.locator(`button[aria-label^="${tenant} / ${env}"]`).first();
  }

  async hoverEnvironmentRow(tenant: string, env: string): Promise<void> {
    await this.envRowButton(tenant, env).hover();
  }

  envHoverCard(tenant: string, env: string): Locator {
    return this.page.getByRole('dialog', { name: `${tenant} / ${env} details` });
  }

  // Scoped through the row button's parent so it resolves one env's dot even
  // when several are open; activating the dot closes that env's tabs.
  envOpenDot(tenant: string, env: string): Locator {
    return this.envRowButton(tenant, env).locator('..').getByTestId('env-open-dot');
  }

  // closeEnvironment activates the row's indicator until the env's tabs are
  // actually gone.
  //
  // One activation is not reliable: the indicator lives inside an IconTooltip,
  // and focusing it opens the tooltip content — a DOM change that can land
  // between the focus and the key, so the key reaches nothing and the env stays
  // open. (Observed on main as well as on a feature branch, on either of the two
  // specs that close an env, roughly one full-suite run in two.) Driven by key
  // rather than mouse because a mouse press hovers first and the tooltip that
  // opens can swallow the click outright.
  //
  // Re-activating until the indicator is gone converges on the observable
  // condition rather than betting on one keypress winning the race. A close that
  // is genuinely broken never converges, so the regression still fails the step.
  // This is the whole close contract for a spec — do not re-assert the indicator
  // afterwards: a closed environment produces no further traffic, so there is no
  // event to bound a "still closed" check against, and a bare re-check is a race
  // with any tab spawn the open left in flight.
  async closeEnvironment(tenant: string, env: string): Promise<void> {
    const dot = this.envOpenDot(tenant, env);
    await expect(async () => {
      if ((await dot.count()) > 0) {
        await dot.focus();
        await dot.press('Enter');
      }
      await expect(dot).toHaveCount(0, { timeout: 2_000 });
    }).toPass({ timeout: 30_000 });
  }

  async hasLocalBadge(tenant: string, env: string): Promise<boolean> {
    const badge = this.envRowButton(tenant, env).locator('[aria-label="Local environment"]');
    return (await badge.count()) > 0;
  }

  // The "(local)" label suffix and the LOCAL pill are both driven by the same
  // isLocal flag, so they must always agree.
  async rowHasLocalSuffix(tenant: string, env: string): Promise<boolean> {
    const label = (await this.envRowButton(tenant, env).getAttribute('aria-label')) ?? '';
    return label.endsWith('(local)');
  }

  cloudAliasButton(): Locator {
    // Matched by position: its label is the user's variable cloud identity, so there is no stable name to query.
    return this.locator().getByRole('button').last();
  }

  cloudAliasRowTrigger(alias: string): Locator {
    return this.locator().getByRole('button', { name: `${alias} cloud status` });
  }

  // One cloud-status row renders per provider type the active tenant references.
  async cloudAliasRowCount(): Promise<number> {
    return this.locator()
      .getByRole('button', { name: /cloud status$/ })
      .count();
  }

  async openCloudAliasPopover(alias: string): Promise<void> {
    await this.cloudAliasRowTrigger(alias).click();
  }

  // The popover is portal'd to document.body, so query it at the page root, not inside the aside.
  cloudAliasPopover(): Locator {
    return this.page.locator('[data-radix-popper-content-wrapper]').first();
  }

  cloudAliasPopoverButton(name: string | RegExp): Locator {
    return this.cloudAliasPopover().getByRole('button', { name });
  }

  // The ERUN section is the top-level host-side control-plane block above
  // ENVIRONMENTS (AI Orchestrators, Platform, Agent-local envs).
  erunSection(): Locator {
    return this.page.getByTestId('erun-section');
  }

  environmentsHeading(): Locator {
    return this.locator().getByText('Environments', { exact: true });
  }

  erunDoctorButton(): Locator {
    return this.erunSection().getByRole('button', { name: 'Run doctor' });
  }

  erunSettingsButton(): Locator {
    return this.erunSection().getByRole('button', { name: 'Open ERun settings' });
  }

  orchestratorsEmptyState(): Locator {
    return this.erunSection().getByText('No orchestrators yet');
  }

  newOrchestratorButton(): Locator {
    return this.erunSection().getByRole('button', { name: 'New orchestrator' });
  }

  // The persistent alert under the orchestrator list: how the operator learns an
  // orchestrator action failed, or that a restart hand-off was refused and the
  // reopened session is idle rather than continuing.
  orchestratorsAlert(): Locator {
    return this.erunSection().getByRole('alert');
  }

  // A persisted orchestrator's row is identified by its "…" details button,
  // whose aria-label carries the name — mirrors environmentRow().
  orchestratorDetailsButton(name: string): Locator {
    return this.erunSection().getByRole('button', { name: `Edit orchestrator ${name} settings` });
  }

  // The orchestrator's Outputs button, mirroring the env row's. Like the other
  // row actions it is pointer-events-none until hover/focus and a hover raises
  // the IconTooltip popper that would swallow a click, so it is driven by
  // keyboard.
  orchestratorOutputsButton(name: string): Locator {
    return this.erunSection().getByRole('button', { name: `Outputs for orchestrator ${name}` });
  }

  async openOrchestratorOutputs(name: string): Promise<void> {
    await this.orchestratorOutputsButton(name).press('Enter');
  }

  // The row's shape-encoded status light, labelled by state, is the same
  // StatusDotGlyph env rows use — so status is never colour-only.
  orchestratorStatusDot(name: string, state: 'running' | 'stopped'): Locator {
    return this.erunSection().getByRole('img', { name: `Orchestrator ${name} is ${state}` });
  }

  // The row's busy spinner (BusyRowSpinner), rendered whenever the store's
  // aiBusyBySession has this orchestrator's session flagged — whether that
  // came from the ai-activity event or from the list snapshot's own busy
  // field.
  orchestratorBusySpinner(name: string): Locator {
    return this.erunSection().getByRole('status', { name: `${name} is working` });
  }

  // The row's background-shell indicator, rendered whenever the store's
  // orchestratorShellActivity.bySession has this orchestrator's session
  // flagged running — from the orchestrator-shell-activity event or from the
  // list snapshot's own shellRunning field, the same treatment
  // orchestratorBusySpinner gets. Matched by prefix since the label carries a
  // live elapsed time.
  orchestratorShellSpinner(name: string): Locator {
    return this.erunSection().getByRole('status', {
      name: new RegExp(`^${name} has a shell running`),
    });
  }

  // Open the orchestrator's management dialog via its "…" button. Like the env
  // edit button it is pointer-events-none until hover/focus and a hover opens
  // the IconTooltip popper that would swallow a click — so focus it and press
  // Enter, retrying because a boot-reattached session can steal focus.
  async openOrchestratorDialog(name: string): Promise<void> {
    const dialog = this.page.getByRole('dialog', { name: 'Edit orchestrator' });
    for (let attempt = 0; attempt < 4; attempt++) {
      await this.orchestratorDetailsButton(name).press('Enter');
      const opened = await dialog.waitFor({ state: 'visible', timeout: 2_000 }).then(
        () => true,
        () => false,
      );
      if (opened) {
        return;
      }
    }
    throw new Error(`orchestrator dialog did not open for ${name} via keyboard`);
  }

  async tenants(): Promise<string[]> {
    const buttons = this.page.locator(
      'button[aria-label^="Collapse "], button[aria-label^="Expand "]',
    );
    const count = await buttons.count();
    const names: string[] = [];
    for (let i = 0; i < count; i++) {
      const label = await buttons.nth(i).getAttribute('aria-label');
      if (!label) continue;
      const name = label.replace(/^(Collapse|Expand) /, '');
      names.push(name);
    }
    return names;
  }

  async environmentsFor(tenant: string): Promise<string[]> {
    // Make sure the tenant is expanded so its env rows are mounted; the
    // edit buttons only exist while the group is open.
    if (!(await this.isTenantExpanded(tenant))) {
      await this.toggleTenant(tenant);
    }
    const buttons = this.page.locator(
      `button[aria-label^="Edit ${tenant} / "][aria-label$=" settings"]`,
    );
    const count = await buttons.count();
    const envs: string[] = [];
    const prefix = `Edit ${tenant} / `;
    const suffix = ' settings';
    for (let i = 0; i < count; i++) {
      const label = await buttons.nth(i).getAttribute('aria-label');
      if (!label || !label.startsWith(prefix) || !label.endsWith(suffix)) continue;
      envs.push(label.slice(prefix.length, label.length - suffix.length));
    }
    return envs;
  }
}
