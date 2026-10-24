import type { Locator, Page } from '@playwright/test';

import type { ElementBox } from '../fixtures/boundingBox.js';

export interface TabBox extends ElementBox {
  label: string;
}

export interface TabStripGeometry {
  list: ElementBox;
  tabs: TabBox[];
}

export type TenantDashboardTab =
  | 'Users'
  | 'Reviews'
  | 'Merge queue'
  | 'Pipeline'
  | 'Builds'
  | 'Audit log'
  | 'Registration'
  | 'Requests'
  | 'API log';

// Replaces the main pane rather than opening a dialog, so waitForOpen anchors on the tab list.
export class TenantDashboard {
  constructor(public readonly page: Page) {}

  async waitForOpen(): Promise<void> {
    await this.page.getByRole('tab', { name: 'Audit log' }).waitFor({ state: 'visible' });
  }

  // Anchors on a tab that survives permission gating.
  async waitForOpenRestricted(): Promise<void> {
    await this.page.getByRole('tab', { name: 'API log' }).waitFor({ state: 'visible' });
  }

  async selectTab(name: TenantDashboardTab): Promise<void> {
    await this.page.getByRole('tab', { name }).click();
  }

  tab(name: TenantDashboardTab): Locator {
    return this.page.getByRole('tab', { name });
  }

  tabs(): Locator {
    return this.page.getByRole('tab');
  }

  // The terminal strip is a second role="tablist"; scope by a dashboard-only tab.
  tabStrip(): Locator {
    return this.page
      .getByRole('tablist')
      .filter({ has: this.page.getByRole('tab', { name: 'API log' }) });
  }

  // One round trip, so all boxes come from the same layout pass.
  async tabStripGeometry(): Promise<TabStripGeometry> {
    return this.tabStrip().evaluate((list) => {
      const box = (el: Element): ElementBox => {
        const rect = el.getBoundingClientRect();
        return { x: rect.x, y: rect.y, width: rect.width, height: rect.height };
      };
      return {
        list: box(list),
        tabs: [...list.querySelectorAll('[role="tab"]')].map((tab) => ({
          label: (tab.textContent ?? '').trim(),
          ...box(tab),
        })),
      };
    });
  }

  // Hit-tests each tab's centre: geometry can't tell which element is on top.
  async coveredTabs(): Promise<string[]> {
    return this.tabStrip().evaluate((list) =>
      [...list.querySelectorAll('[role="tab"]')].flatMap((tab) => {
        const rect = tab.getBoundingClientRect();
        const hit = document.elementFromPoint(rect.x + rect.width / 2, rect.y + rect.height / 2);
        if (hit && (hit === tab || tab.contains(hit))) {
          return [];
        }
        const label = (tab.textContent ?? '').trim();
        const blocker = hit
          ? `${hit.tagName}${hit.textContent ? ` "${hit.textContent.trim()}"` : ''}`
          : 'nothing';
        return [`${label} is covered at its own centre by ${blocker}`];
      }),
    );
  }

  // The grant renders as a sibling of the opening sentence, so locate the whole region.
  restrictedAccessNote(): Locator {
    return this.page
      .getByText('Some panels are hidden because you do not have access to')
      .locator('..');
  }

  async clickRefresh(): Promise<void> {
    await this.page.getByRole('button', { name: 'Refresh', exact: true }).click();
  }

  activePanel(): Locator {
    return this.page.getByRole('tabpanel');
  }

  buildProfileButtonFor(buildId: string): Locator {
    return this.activePanel().getByRole('button', { name: `View build profile for ${buildId}` });
  }

  auditTable(): Locator {
    return this.activePanel().getByRole('table');
  }

  auditRows(): Locator {
    return this.auditTable().locator('tbody tr');
  }

  auditEmptyState(): Locator {
    return this.activePanel().getByText('No audit events', { exact: true });
  }

  // A failed read renders an alert; an empty log renders the empty state.
  apiLogAlert(): Locator {
    return this.activePanel().getByRole('alert');
  }

  apiLogEmptyState(): Locator {
    return this.activePanel().getByText('No API log returned', { exact: true });
  }

  apiLogBody(): Locator {
    return this.activePanel().locator('pre');
  }

  usersTable(): Locator {
    return this.activePanel().getByRole('table');
  }

  usersRows(): Locator {
    return this.usersTable().locator('tbody tr');
  }

  usersEmptyState(): Locator {
    return this.activePanel().getByText('No signed-in user', { exact: true });
  }

  pipelineTable(): Locator {
    return this.activePanel().getByRole('table');
  }

  pipelineRows(): Locator {
    return this.pipelineTable().locator('tbody tr');
  }

  pipelineEmptyState(): Locator {
    return this.activePanel().getByText('Nothing is in the pipeline', { exact: true });
  }

  reviewsTable(): Locator {
    return this.activePanel().getByRole('table');
  }

  reviewsRows(): Locator {
    return this.reviewsTable().locator('tbody tr');
  }

  reviewsEmptyState(): Locator {
    return this.activePanel().getByText('No reviews yet', { exact: true });
  }

  // Must read differently from reviewsEmptyState ("nothing exists yet").
  reviewsFilteredEmptyState(): Locator {
    return this.activePanel().getByText('No reviews match this filter', { exact: true });
  }

  clearReviewFilterButton(): Locator {
    return this.activePanel().getByRole('button', { name: 'Clear filter' });
  }

  // Prefix match: the name carries a count badge ("Mine 2").
  mineFilterButton(): Locator {
    return this.activePanel().getByRole('button', { name: /^Mine\b/ });
  }

  waitingOnMeFilterButton(): Locator {
    return this.activePanel().getByRole('button', { name: 'Waiting on me' });
  }

  // Prefix match: the name carries a count badge ("OPEN 3").
  reviewStatusFilterButton(status: string): Locator {
    return this.activePanel().getByRole('button', { name: new RegExp(`^${status}\\b`) });
  }

  reviewStatusFilterGroup(): Locator {
    return this.activePanel().getByRole('group', { name: 'Filter reviews by status' });
  }

  reviewCountText(): Locator {
    return this.activePanel().getByText(/^\d+( of \d+)? reviews?$/);
  }

  async openReview(name: string): Promise<void> {
    await this.page.getByRole('button', { name: `Open review ${name}` }).click();
  }

  newReviewButton(): Locator {
    return this.activePanel().getByRole('button', { name: 'New review' });
  }

  reviewsRestrictedNote(): Locator {
    return this.activePanel().getByText('You do not have access to create reviews.');
  }

  mergeQueueTable(): Locator {
    return this.activePanel().getByRole('table');
  }

  mergeQueueRows(): Locator {
    return this.mergeQueueTable().locator('tbody tr');
  }

  advanceMergeQueueButton(): Locator {
    return this.activePanel().getByRole('button', { name: 'Advance queue' });
  }

  advanceMergeQueueConfirmButton(): Locator {
    return this.activePanel().getByRole('button', { name: 'Confirm' });
  }

  advanceMergeQueueRestrictedNote(): Locator {
    return this.activePanel().getByText('You do not have access to advance the merge queue.');
  }

  // A queue spanning several branches or repositories has no single head to advance.
  advanceMergeQueueMixedBranchNote(): Locator {
    return this.activePanel().getByText(
      'These reviews do not all name one branch and repository, so there is no single queue head to advance.',
    );
  }

  mergeQueueBlockedAlert(): Locator {
    return this.activePanel().getByRole('alert');
  }

  mergeQueueViewDiscussionButton(): Locator {
    return this.activePanel().getByRole('button', { name: 'View discussion' });
  }

  mergeQueueOverrideAnywayButton(): Locator {
    return this.activePanel().getByRole('button', { name: 'Override anyway' });
  }

  mergeQueueOverrideReasonInput(): Locator {
    return this.activePanel().getByLabel('Reason for overriding');
  }

  mergeQueueOverrideCancelButton(): Locator {
    return this.activePanel().getByRole('button', { name: 'Cancel' });
  }

  mergeQueueOverrideConfirmButton(): Locator {
    return this.activePanel().getByRole('button', { name: 'Confirm override' });
  }

  // None of these render a tab strip; EmptyState's heading is a plain div.
  notConnectedHeading(): Locator {
    return this.page.getByText('Connect this tenant to erunpaas.com', { exact: true });
  }

  // The body must not name a subset of tabs, which would imply the rest are usable.
  notConnectedBody(): Locator {
    return this.page.getByText(/This tenant isn't connected to a hosted erun platform yet/);
  }

  connectApiUrlInput(): Locator {
    return this.page.getByLabel('Platform API URL');
  }

  connectButton(): Locator {
    return this.page.getByRole('button', { name: 'Connect', exact: true });
  }

  // By id: the sidebar may show its own alert at the same time.
  connectErrorAlert(): Locator {
    return this.page.locator('#platform-connect-error');
  }

  chooseAliasHeading(): Locator {
    return this.page.getByText('Choose which platform to use', { exact: true });
  }

  chooseAliasButton(alias: string): Locator {
    return this.page.getByRole('button', { name: alias, exact: true });
  }

  notSignedInHeading(): Locator {
    return this.page.getByText('Sign in to the erun platform', { exact: true });
  }

  signInButton(): Locator {
    return this.page.getByRole('button', { name: 'Log in' });
  }

  notEnrolledHeading(): Locator {
    return this.page.getByText("This identity isn't enrolled in this tenant yet", { exact: true });
  }

  // Not exact: FieldLabel appends a visually-hidden "(required)".
  enrollUsernameInput(): Locator {
    return this.page.getByLabel('Username');
  }

  tryEnrollButton(): Locator {
    return this.page.getByRole('button', { name: 'Try to enroll myself' });
  }

  enrollAdminCommand(): Locator {
    return this.page.locator('#enroll-admin-command');
  }

  // By id for the same reason as connectErrorAlert.
  enrollErrorAlert(): Locator {
    return this.page.locator('#platform-enroll-error');
  }

  noPermissionHeading(): Locator {
    return this.page.getByText("You do not have access to this tenant's dashboard", {
      exact: true,
    });
  }

  // The whole card is the locator: its body names both tenants.
  tenantMismatchHeading(): Locator {
    return this.page.getByText('This platform connection belongs to a different tenant', {
      exact: true,
    });
  }

  tenantMismatchBody(): Locator {
    return this.page.getByText(/belongs to the platform tenant/);
  }

  platformContactLine(): Locator {
    return this.page.getByText('Platform:', { exact: false });
  }

  // The only place the surface says whether the platform tenant is the local one.
  platformTenantLine(): Locator {
    return this.page.getByText('Platform tenant:', { exact: false });
  }

  twoRegistriesNotice(): Locator {
    return this.activePanel().getByText('live in this machine', { exact: false });
  }

  contextsHeading(): Locator {
    return this.activePanel().getByRole('heading', { name: 'Cloud contexts' });
  }

  contextsEmptyState(): Locator {
    return this.activePanel().getByText('No cloud contexts registered', { exact: true });
  }

  // Not exact: FieldLabel appends "(required)"; .first() is the context form's field.
  contextNameInput(): Locator {
    return this.activePanel().getByLabel('Name').first();
  }

  contextAliasInput(): Locator {
    return this.activePanel().getByLabel('Cloud provider alias');
  }

  contextRegionInput(): Locator {
    return this.activePanel().getByLabel('Region');
  }

  previewContextButton(): Locator {
    return this.activePanel().getByRole('button', { name: 'Preview context plan' });
  }

  registerContextButton(): Locator {
    return this.activePanel().getByRole('button', { name: 'Register context' });
  }

  environmentsHeading(): Locator {
    return this.activePanel().getByRole('heading', { name: 'Hosted environments' });
  }

  environmentsEmptyState(): Locator {
    return this.activePanel().getByText('No hosted environments registered', { exact: true });
  }

  // .last() picks the environment form over the context form's "Name".
  envNameInput(): Locator {
    return this.activePanel().getByLabel('Name').last();
  }

  envAdoptToggle(): Locator {
    return this.activePanel().getByRole('checkbox', { name: /already exists/ });
  }

  envKubernetesContextInput(): Locator {
    return this.activePanel().getByLabel('Kubernetes context', { exact: false });
  }

  previewEnvironmentButton(): Locator {
    return this.activePanel().getByRole('button', { name: 'Preview plan' });
  }

  registerEnvironmentButton(): Locator {
    return this.activePanel().getByRole('button', { name: 'Register environment' });
  }

  recordEnvironmentButton(): Locator {
    return this.activePanel().getByRole('button', { name: 'Record environment' });
  }

  localEnvironmentsHeading(): Locator {
    return this.activePanel().getByRole('heading', { name: 'Your local environments' });
  }

  putOnPlatformButtonFor(name: string): Locator {
    return this.activePanel()
      .locator('li')
      .filter({ hasText: name })
      .getByRole('button', { name: 'Put on platform' });
  }

  registrationRestrictedNote(): Locator {
    return this.activePanel().getByText('Registering anything on the platform needs');
  }

  environmentsTable(): Locator {
    return this.activePanel().getByRole('table').last();
  }

  environmentRow(name: string): Locator {
    return this.environmentsTable().locator('tbody tr').filter({ hasText: name });
  }

  // The alert must wrap long payloads rather than ellipsising them.
  environmentRowAlert(name: string): Locator {
    return this.environmentRow(name).getByRole('alert');
  }

  deployButtonFor(name: string): Locator {
    return this.environmentRow(name).getByRole('button', { name: 'Deploy' });
  }

  stopButtonFor(name: string): Locator {
    return this.environmentRow(name).getByRole('button', { name: 'Stop' });
  }

  // DeleteControl keeps the "Delete" label through confirmation.
  deleteButtonFor(name: string): Locator {
    return this.environmentRow(name).getByRole('button', { name: 'Delete', exact: true });
  }

  deleteConfirmInputFor(name: string): Locator {
    return this.environmentRow(name).getByRole('textbox');
  }

  deleteCancelButtonFor(name: string): Locator {
    return this.environmentRow(name).getByRole('button', { name: 'Cancel' });
  }

  // Expected outcomes render role="status", never role="alert".
  recoverableNote(text: string): Locator {
    return this.activePanel().getByText(text, { exact: false });
  }

  requestInvitationButton(): Locator {
    return this.page.getByRole('button', { name: 'Request an invitation' });
  }

  requestAgainButton(): Locator {
    return this.page.getByRole('button', { name: 'Request again' });
  }

  requestInvitationDialog(): Locator {
    return this.page.getByRole('dialog', { name: 'Request an invitation' });
  }

  requestInvitationIdentitySummary(): Locator {
    // exact: the dialog description also contains this substring.
    return this.requestInvitationDialog().getByText('Your verified identity', { exact: true });
  }

  requestKindJoinTab(): Locator {
    return this.requestInvitationDialog().getByRole('tab', { name: 'Join an existing tenant' });
  }

  requestKindCreateTab(): Locator {
    return this.requestInvitationDialog().getByRole('tab', { name: 'Register a new tenant' });
  }

  requestNoteInput(): Locator {
    return this.requestInvitationDialog().getByLabel('Note to the operator (optional)');
  }

  requestSubmitButton(): Locator {
    return this.requestInvitationDialog().getByRole('button', { name: /Send request|Sending/ });
  }

  requestCancelButton(): Locator {
    return this.requestInvitationDialog().getByRole('button', { name: 'Cancel' });
  }

  requestSubmitDisabledReason(): Locator {
    return this.requestInvitationDialog().locator('#request-invitation-submit-reason');
  }

  requestErrorAlert(): Locator {
    return this.requestInvitationDialog().getByRole('alert');
  }

  requestPendingStatus(): Locator {
    return this.page.getByText('Pending', { exact: true });
  }

  requestDeclinedStatus(): Locator {
    return this.page.getByText('Declined', { exact: true });
  }

  // A transport failure must never read as "never requested".
  // No name filter: the alert role does not compute a name from content.
  requestStatusCheckFailedAlert(): Locator {
    return this.page.getByRole('alert').filter({ hasText: 'invitation request status' });
  }

  requestStatusCheckRetryButton(): Locator {
    return this.page.getByRole('button', { name: 'Try again' });
  }

  requestsTable(): Locator {
    return this.activePanel().getByRole('table');
  }

  requestsEmptyState(): Locator {
    return this.activePanel().getByText('No pending requests', { exact: true });
  }

  requestsPermissionNotice(): Locator {
    return this.activePanel().getByText(
      'You can see this queue, but issuing invitations or declining requests needs additional access.',
    );
  }

  requestRowFor(subject: string): Locator {
    return this.requestsTable().locator('tbody tr').filter({ hasText: subject });
  }

  issueInvitationButtonFor(subject: string): Locator {
    return this.requestRowFor(subject).getByRole('button', { name: /Issue invitation/ });
  }

  declineButtonFor(subject: string): Locator {
    return this.requestRowFor(subject).getByRole('button', { name: 'Decline' });
  }

  requestRowErrorAlert(subject: string): Locator {
    return this.requestRowFor(subject).getByRole('alert');
  }

  declineRequestDialog(): Locator {
    return this.page.getByRole('dialog', { name: 'Decline this request?' });
  }

  declineReasonInput(): Locator {
    return this.declineRequestDialog().getByPlaceholder('Why is this request being declined?');
  }

  declineConfirmButton(): Locator {
    return this.declineRequestDialog().getByRole('button', { name: 'Decline' });
  }

  declineCancelButton(): Locator {
    return this.declineRequestDialog().getByRole('button', { name: 'Cancel' });
  }

  issuedInviteLinkNotice(): Locator {
    return this.activePanel().getByText('Invitation issued:', { exact: false });
  }
}
