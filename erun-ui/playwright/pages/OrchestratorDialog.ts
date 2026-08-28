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

  // Takes the mode for the same reason locator/waitForOpen/waitForClosed do:
  // defaulting to 'New orchestrator' silently aimed at a dialog that is not
  // open, so an Edit-mode caller waited out the full timeout on a Cancel
  // button that was never going to appear.
  async cancel(mode: 'New orchestrator' | 'Edit orchestrator' = 'New orchestrator'): Promise<void> {
    await this.locator(mode).getByRole('button', { name: 'Cancel' }).click();
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

  // The conversation picker: which conversation this orchestrator resumes, every
  // one it could be pointed at instead, and the attach that corrects it. Rows
  // carry their conversation id and role as data attributes so a spec addresses
  // one without matching on rendered prose.
  conversationSummary(): Locator {
    return this.locator('Edit orchestrator').getByText('Resuming the conversation', {
      exact: false,
    });
  }

  conversationRow(conversationId: string): Locator {
    return this.locator('Edit orchestrator').locator(`[data-conversation-id="${conversationId}"]`);
  }

  conversationRows(): Locator {
    return this.locator('Edit orchestrator').locator('[data-conversation-id]');
  }

  conversationAttachButton(conversationId: string): Locator {
    return this.conversationRow(conversationId).getByRole('button', {
      name: `Attach ${conversationId} to this orchestrator`,
    });
  }

  conversationDetachButton(conversationId: string): Locator {
    return this.conversationRow(conversationId).getByRole('button', {
      name: `Stop using ${conversationId} for this orchestrator`,
    });
  }

  // The restart-required banner (erun#1319): shown at the top of the form when
  // this orchestrator's live session was spawned with a scope that no longer
  // matches what it is linked to now, naming why and carrying its own action
  // rather than pointing at the footer button below.
  restartRequiredNotice(): Locator {
    return this.locator('Edit orchestrator').getByRole('status').filter({
      hasText: 'Its environments changed while it was running',
    });
  }

  restartNowButton(): Locator {
    return this.restartRequiredNotice().getByRole('button', { name: 'Restart now' });
  }

  // The footer's own restart action, present whenever the session is running;
  // its label names the remedy explicitly once a restart is actually required.
  footerRestartButton(): Locator {
    return this.locator('Edit orchestrator').getByRole('button', {
      name: /^Restart( to apply)?$/,
    });
  }
}
