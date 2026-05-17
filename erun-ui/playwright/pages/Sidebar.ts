import type { Locator, Page } from '@playwright/test';

// Sidebar POM. The sidebar is rendered as <aside> (implicit ARIA
// complementary) and exposes:
//  - tenant rows whose toggle button is labelled
//    "Expand <tenant>"  when collapsed
//    "Collapse <tenant>" when expanded
//  - environment rows reachable through the per-env manage button labelled
//    "Edit <tenant> / <env> settings".
export class Sidebar {
  constructor(public readonly page: Page) {}

  locator(): Locator {
    return this.page.locator('aside').first();
  }

  async openSettings(): Promise<void> {
    await this.page.getByRole('button', { name: 'Open ERun settings' }).click();
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
    // The env-row button is unlabelled; its accessible name is computed
    // from inner span text (env name plus the optional "Local" badge), so
    // an exact match would miss local envs. Match by title attribute,
    // which the component sets to "<tenant> / <env>" (with a "(local)"
    // suffix for local envs).
    await this.page
      .locator(`button[title^="${tenant} / ${env}"]`)
      .first()
      .click();
  }

  async openManageDialogFor(tenant: string, env: string): Promise<void> {
    await this.environmentRow(tenant, env).click();
  }

  cloudAliasButton(): Locator {
    // The bottom-of-sidebar control is a popover trigger labelled with the
    // user's cloud identity; it's the last button in the aside.
    return this.locator().getByRole('button').last();
  }

  async tenants(): Promise<string[]> {
    // The toggle button's aria-label is "Collapse <name>" or "Expand
    // <name>"; strip the prefix to recover the tenant name in DOM order.
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
    const buttons = this.page.locator(`button[aria-label^="Edit ${tenant} / "][aria-label$=" settings"]`);
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
