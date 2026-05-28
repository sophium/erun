import type { Locator, Page } from '@playwright/test';

export type ManageTab = 'General' | 'Runtime' | 'AI' | 'Ports' | 'SSH';

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

  // isRemoteEnv returns true when the env type is anything other than the
  // local-agent variant. Mirrors the semantic the old remoteFieldValue
  // helper provided, but reads from the new Environment type field which is
  // the canonical control after #375 dropped the editable Remote toggle.
  async isRemoteEnv(): Promise<boolean> {
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
}
