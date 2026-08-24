import type { Locator, Page } from '@playwright/test';

// OrchestratorDialog is the create/edit surface for a host-side orchestrator: a
// name plus the agent environments it links, each with the host directory it is
// reviewed in. The dialog title differs between the two modes, so callers say
// which one they opened.
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

  // Each candidate renders as a checkbox row labelled "<tenant> / <environment>"
  // followed by which kind of review directory it has.
  envRow(tenant: string, environment: string): Locator {
    return this.locator()
      .locator('label')
      .filter({ hasText: `${tenant} / ${environment}` });
  }

  envCheckbox(tenant: string, environment: string): Locator {
    return this.envRow(tenant, environment).getByRole('checkbox');
  }

  async toggleEnv(tenant: string, environment: string): Promise<void> {
    await this.envCheckbox(tenant, environment).click();
  }

  // The row's own container, which holds the review-directory controls the
  // checkbox reveals.
  envBlock(tenant: string, environment: string): Locator {
    return this.locator()
      .locator('div')
      .filter({ hasText: `${tenant} / ${environment}` })
      .last();
  }

  // Present only for a mirrored env, whose directory the operator may place
  // anywhere. A local-agent env's directory is derived and rendered as text.
  envDirectoryInput(tenant: string, environment: string): Locator {
    return this.envBlock(tenant, environment).getByRole('textbox');
  }

  envChooseDirectoryButton(tenant: string, environment: string): Locator {
    return this.locator().getByRole('button', {
      name: `Choose sync directory for ${tenant} / ${environment}`,
    });
  }

  async cancel(): Promise<void> {
    await this.locator().getByRole('button', { name: 'Cancel' }).click();
  }

  nameInput(): Locator {
    return this.locator().getByLabel('Name');
  }

  createButton(): Locator {
    return this.locator().getByRole('button', { name: 'Create' });
  }

  async create(name: string): Promise<void> {
    await this.nameInput().fill(name);
    await this.createButton().click();
  }

  // The two guidance layers (#1231): "role" is this orchestrator's own
  // CLAUDE.<id>.md, "shared" is the one CLAUDE.md every orchestrator obeys.
  // Each renders its resolved host path as secondary text and a reveal
  // button per IDE, labelled with this same title text. Guidance only renders
  // for a persisted (non-transient) orchestrator, i.e. only in Edit mode.
  private static readonly guidanceTitle: Record<'role' | 'shared', string> = {
    role: 'Role: what this orchestrator does',
    shared: 'Shared contract: rules for every orchestrator',
  };

  guidanceLabel(layer: 'role' | 'shared'): Locator {
    return this.locator('Edit orchestrator').getByText(OrchestratorDialog.guidanceTitle[layer], {
      exact: true,
    });
  }

  // The role path is this orchestrator's own CLAUDE.<id>.md; the shared path
  // is the plain CLAUDE.md every orchestrator obeys — distinguishing substrings
  // so each resolves to exactly one element without scoping into row markup.
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
}
