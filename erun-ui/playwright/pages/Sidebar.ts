import { expect, type Locator, type Page } from '@playwright/test';

import { injectTestStylesheet } from './testStylesheet';

// Keys the injected rule so a repeat injection is a no-op.
const SPINNER_ANIMATION_OFF_ATTR = 'data-erun-test-spinner-animation-off';
const SPINNER_ANIMATION_OFF = '.animate-spin { animation: none !important; }';

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

  runDoctorButton(): Locator {
    return this.page.getByRole('button', { name: 'Run doctor' });
  }

  documentationButton(): Locator {
    return this.page.getByRole('button', { name: 'Open documentation' });
  }

  upgradeAllDialog(): Locator {
    return this.page.getByRole('dialog', { name: 'Upgrade all environments' });
  }

  async openInitDialog(): Promise<void> {
    const button = this.page.getByRole('button', { name: 'Initialize new environment' });
    if (await button.isVisible().catch(() => false)) {
      await button.click();
      return;
    }
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

  async isTenantExpanded(name: string): Promise<boolean> {
    const value = await this.tenantRow(name).first().getAttribute('aria-expanded');
    return value === 'true';
  }

  environmentRow(tenant: string, env: string): Locator {
    // The aria-label carries both names; env names repeat across tenants.
    return this.page.getByRole('button', { name: `Edit ${tenant} / ${env} settings` });
  }

  async openEnvironment(tenant: string, env: string): Promise<void> {
    // Prefix-match: local envs append a "(local)" suffix.
    await this.page.locator(`button[aria-label^="${tenant} / ${env}"]`).first().click();
  }

  async openManageDialogFor(tenant: string, env: string): Promise<void> {
    await this.openManageDialogViaKeyboard(tenant, env);
  }

  // The Outputs button stops propagation, so it never also opens the env.
  async openOutputs(tenant: string, env: string): Promise<void> {
    await this.hoverEnvironmentRow(tenant, env);
    await this.page.getByRole('button', { name: `Outputs for ${tenant} / ${env}` }).click();
  }

  // Despite the name, activation is a dispatched click: a mouse press raises a
  // tooltip that intercepts it and a key press races terminal focus.
  // toPass stays bare so the calling test's deadline bounds it; `{ timeout }` would cap it.
  // Click only while the button exists: the open modal aria-hides it, and a locator
  // action on a missing element waits out the whole test budget.
  async openManageDialogViaKeyboard(tenant: string, env: string): Promise<void> {
    const dialog = this.page
      .getByRole('dialog')
      .filter({ has: this.page.getByRole('tab', { name: /^General/ }) })
      .first();
    let activated = false;
    await expect(async () => {
      const edit = this.environmentRow(tenant, env);
      if ((await edit.count()) > 0) {
        activated = true;
        await edit.dispatchEvent('click');
      }
      if (!activated) {
        throw new Error(
          `${tenant} / ${env} has no reachable edit button, so this step would activate nothing`,
        );
      }
      await dialog.waitFor({ state: 'visible', timeout: 2_000 });
    }).toPass();
  }

  // The clickable row button, not the edit button environmentRow() returns.
  envRowButton(tenant: string, env: string): Locator {
    return this.page.locator(`button[aria-label^="${tenant} / ${env}"]`).first();
  }

  // The card's open state is row-local and nothing reopens it while the pointer stays,
  // so move away and re-hover until the card is visible.
  async hoverEnvironmentRow(tenant: string, env: string): Promise<void> {
    await expect(async () => {
      await this.page.mouse.move(0, 0);
      await this.envRowButton(tenant, env).hover();
      await this.envHoverCard(tenant, env).waitFor({ state: 'visible', timeout: 2_000 });
    }).toPass();
  }

  envHoverCard(tenant: string, env: string): Locator {
    return this.page.getByRole('dialog', { name: `${tenant} / ${env} details` });
  }

  // Hover and read retry as one unit so a card released mid-read is re-hovered.
  async readEnvHoverCard(
    tenant: string,
    env: string,
    read: (card: Locator) => Promise<void>,
  ): Promise<void> {
    await expect(async () => {
      await this.hoverEnvironmentRow(tenant, env);
      await read(this.envHoverCard(tenant, env));
    }).toPass();
  }

  // Activating the dot closes that env's tabs.
  envOpenDot(tenant: string, env: string): Locator {
    return this.envRowButton(tenant, env).locator('..').getByTestId('env-open-dot');
  }

  // Reports the cloud node, not the environment; asserted apart from envOpenDot.
  envNodeIndicator(tenant: string, env: string): Locator {
    return this.envRowButton(tenant, env).locator('..').getByTestId('env-node-indicator');
  }

  // Same testid renders as a close control only while data-env-opened="true", else a
  // passive status light, so "no dot" is ambiguous right after openEnvironment.
  envCloseControl(tenant: string, env: string): Locator {
    return this.envRowButton(tenant, env)
      .locator('..')
      .locator('[data-testid="env-open-dot"][data-env-opened="true"]');
  }

  // Presses once, for specs that hold the close RPC open and so never see the row close.
  async activateEnvCloseControl(tenant: string, env: string): Promise<void> {
    const control = this.envCloseControl(tenant, env);
    await control.waitFor({ state: 'visible' });
    await control.focus();
    await control.press('Enter');
  }

  // Re-activates until the tabs are gone: the tooltip opening on focus can swallow the key.
  // This is the whole close contract; don't re-assert afterwards. Bare toPass so the
  // caller's deadline bounds the wait for CloseEnvironmentSessions.
  async closeEnvironment(tenant: string, env: string): Promise<void> {
    const dot = this.envOpenDot(tenant, env);
    // A quiet row counts as closed only after the control was seen.
    const control = this.envCloseControl(tenant, env);
    let sawControl = false;
    await expect(async () => {
      if ((await control.count()) > 0) {
        sawControl = true;
        await control.focus();
        await control.press('Enter');
      }
      if (!sawControl) {
        throw new Error(
          `${tenant} / ${env} is not reported open here, so this close would press nothing`,
        );
      }
      await expect(dot).toHaveCount(0, { timeout: 2_000 });
    }).toPass();
  }

  async hasLocalBadge(tenant: string, env: string): Promise<boolean> {
    const badge = this.envRowButton(tenant, env).locator('[aria-label="Local environment"]');
    return (await badge.count()) > 0;
  }

  // The "(local)" suffix and LOCAL pill share one isLocal flag.
  async rowHasLocalSuffix(tenant: string, env: string): Promise<boolean> {
    const label = (await this.envRowButton(tenant, env).getAttribute('aria-label')) ?? '';
    return label.endsWith('(local)');
  }

  // hasLocalBadge and hasHostBadge must never both be true for a row.
  async hasHostBadge(tenant: string, env: string): Promise<boolean> {
    const badge = this.envRowButton(tenant, env).locator(
      '[aria-label="Host environment — no pod, this machine only"]',
    );
    return (await badge.count()) > 0;
  }

  async rowHasHostSuffix(tenant: string, env: string): Promise<boolean> {
    const label = (await this.envRowButton(tenant, env).getAttribute('aria-label')) ?? '';
    return label.endsWith('(host)');
  }

  cloudAliasButton(): Locator {
    // Matched by position: the label is the user's cloud identity.
    return this.locator().getByRole('button').last();
  }

  cloudAliasRowTrigger(alias: string): Locator {
    return this.locator().getByRole('button', { name: `${alias} cloud status` });
  }

  async cloudAliasRowCount(): Promise<number> {
    return this.locator()
      .getByRole('button', { name: /cloud status$/ })
      .count();
  }

  async openCloudAliasPopover(alias: string): Promise<void> {
    await this.cloudAliasRowTrigger(alias).click();
  }

  // Portaled to document.body.
  cloudAliasPopover(): Locator {
    return this.page.locator('[data-radix-popper-content-wrapper]').first();
  }

  cloudAliasPopoverButton(name: string | RegExp): Locator {
    return this.cloudAliasPopover().getByRole('button', { name });
  }

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

  orchestratorsAlert(): Locator {
    return this.erunSection().getByRole('alert');
  }

  orchestratorDetailsButton(name: string): Locator {
    return this.erunSection().getByRole('button', { name: `Edit orchestrator ${name} settings` });
  }

  orchestratorRowButton(name: string): Locator {
    return this.erunSection().getByRole('button', {
      name: new RegExp(`^(Open|Start) orchestrator ${name}$`),
    });
  }

  async openOrchestratorSession(name: string): Promise<void> {
    await this.orchestratorRowButton(name).click();
  }

  orchestratorHoverCard(name: string): Locator {
    return this.page.getByRole('dialog', { name: `${name} details` });
  }

  // Convergent and bare for the same reasons as readEnvHoverCard.
  async readOrchestratorHoverCard(
    name: string,
    read: (card: Locator) => Promise<void>,
  ): Promise<void> {
    await expect(async () => {
      await this.hoverOrchestratorRow(name);
      await read(this.orchestratorHoverCard(name));
    }).toPass();
  }

  // Convergent and bare for the same reasons as hoverEnvironmentRow.
  async hoverOrchestratorRow(name: string): Promise<void> {
    await expect(async () => {
      await this.page.mouse.move(0, 0);
      await this.orchestratorRowButton(name).hover();
      await this.orchestratorHoverCard(name).waitFor({ state: 'visible', timeout: 2_000 });
    }).toPass();
  }

  tenantDashboardButton(tenant: string): Locator {
    return this.page.getByRole('button', { name: `Open ${tenant} dashboard` });
  }

  async openTenantDashboard(tenant: string): Promise<void> {
    await this.tenantDashboardButton(tenant).click();
  }

  // Driven by keyboard: pointer-events-none until hover, and hover raises a click-swallowing tooltip.
  orchestratorOutputsButton(name: string): Locator {
    return this.erunSection().getByRole('button', { name: `Outputs for orchestrator ${name}` });
  }

  async openOrchestratorOutputs(name: string): Promise<void> {
    await this.orchestratorOutputsButton(name).press('Enter');
  }

  // Exact match: the restart-required label starts with the plain running label.
  orchestratorStatusDot(name: string, state: 'running' | 'stopped'): Locator {
    return this.erunSection().getByRole('img', {
      name: `Orchestrator ${name} is ${state}`,
      exact: true,
    });
  }

  // A session whose env scope changed while running keeps its old toolset until restarted.
  orchestratorRestartRequiredDot(name: string): Locator {
    return this.erunSection().getByRole('img', {
      name: `Orchestrator ${name} is running but needs a restart to apply its new environments`,
    });
  }

  orchestratorBusySpinner(name: string): Locator {
    return this.erunSection().getByRole('status', { name: `${name} is working` });
  }

  // The spinning icon never passes Playwright's hover stability check, so freeze it first.
  // Uses injectTestStylesheet because a stalled addStyleTag has no timeout of its own.
  async hoverOrchestratorBusySpinner(name: string): Promise<void> {
    const spinner = this.orchestratorBusySpinner(name);
    await spinner.waitFor({ state: 'visible' });
    await injectTestStylesheet(this.page, SPINNER_ANIMATION_OFF_ATTR, SPINNER_ANIMATION_OFF);
    await spinner.hover();
  }

  orchestratorBusyTooltip(name: string): Locator {
    return this.page.getByRole('tooltip', { name: `${name} is working` });
  }

  // Prefix match: the label carries a live elapsed time.
  orchestratorShellSpinner(name: string): Locator {
    return this.erunSection().getByRole('status', {
      name: new RegExp(`^${name} has a shell running`),
    });
  }

  // Focus and Enter, since a hover raises a click-swallowing tooltip; retried in a bare
  // toPass because a boot-reattached session can steal focus.
  async openOrchestratorDialog(name: string): Promise<void> {
    const dialog = this.page.getByRole('dialog', { name: 'Edit orchestrator' });
    await expect(async () => {
      await this.orchestratorDetailsButton(name).press('Enter');
      await dialog.waitFor({ state: 'visible', timeout: 2_000 });
    }).toPass();
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

  // Enrolled renders a button, the other states a popover trigger; one locator covers both.
  tenantEnrollmentStatus(tenant: string): Locator {
    return this.page
      .getByTestId('tenant-enrollment-status')
      .and(this.page.locator(`[aria-label*="${tenant}"]`));
  }

  async openTenantEnrollmentStatusPopover(tenant: string): Promise<void> {
    await this.tenantEnrollmentStatus(tenant).click();
  }

  // Portaled to document.body.
  tenantEnrollmentStatusPopover(): Locator {
    return this.page.locator('[data-radix-popper-content-wrapper]').first();
  }

  async environmentsFor(tenant: string): Promise<string[]> {
    // Env rows mount only while the tenant group is expanded.
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
