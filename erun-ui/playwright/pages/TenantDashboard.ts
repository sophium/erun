import type { Locator, Page } from '@playwright/test';

export type TenantDashboardTab =
  | 'Users'
  | 'Reviews'
  | 'Merge queue'
  | 'Gates'
  | 'Builds'
  | 'Audit log'
  | 'Registration'
  | 'Requests'
  | 'API log';

// TenantDashboard POM. Unlike the dialogs, this view replaces the main pane
// content (MainPane.tsx) rather than rendering inside a Radix dialog, so
// waitForOpen anchors on the tab list rather than a role="dialog".
export class TenantDashboard {
  constructor(public readonly page: Page) {}

  async waitForOpen(): Promise<void> {
    await this.page.getByRole('tab', { name: 'Audit log' }).waitFor({ state: 'visible' });
  }

  // waitForOpenRestricted anchors on a tab that survives permission gating, for
  // the cases where the dashboard's other tabs are deliberately absent.
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

  restrictedAccessNote(): Locator {
    return this.page.getByText('Some panels are hidden because you do not have access to');
  }

  async clickRefresh(): Promise<void> {
    await this.page.getByRole('button', { name: 'Refresh', exact: true }).click();
  }

  activePanel(): Locator {
    return this.page.getByRole('tabpanel');
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

  gatesTable(): Locator {
    return this.activePanel().getByRole('table');
  }

  gatesRows(): Locator {
    return this.gatesTable().locator('tbody tr');
  }

  gatesEmptyState(): Locator {
    return this.activePanel().getByText('No gate runs yet', { exact: true });
  }

  usersTable(): Locator {
    return this.activePanel().getByRole('table');
  }

  usersRows(): Locator {
    return this.usersTable().locator('tbody tr');
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

  // reviewsFilteredEmptyState is the distinct "nothing matches this filter"
  // empty state (as opposed to reviewsEmptyState's "nothing exists yet") —
  // the repo's three-empty-states rule requires the two read differently.
  reviewsFilteredEmptyState(): Locator {
    return this.activePanel().getByText('No reviews match this filter', { exact: true });
  }

  clearReviewFilterButton(): Locator {
    return this.activePanel().getByRole('button', { name: 'Clear filter' });
  }

  // Mine's accessible name gains a count badge (e.g. "Mine 2"), so this
  // matches the prefix rather than the exact former label (#1378).
  mineFilterButton(): Locator {
    return this.activePanel().getByRole('button', { name: /^Mine\b/ });
  }

  waitingOnMeFilterButton(): Locator {
    return this.activePanel().getByRole('button', { name: 'Waiting on me' });
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

  // A queue spanning several target branches has no single head, so the action
  // is replaced by the reason rather than silently absent.
  advanceMergeQueueMixedBranchNote(): Locator {
    return this.activePanel().getByText(
      'These reviews target more than one branch, so there is no single queue head to advance.',
    );
  }

  // mergeQueueBlockedAlert is AdvanceMergeQueue's unresolved-thread refusal,
  // rendered as role="alert" (InlineAlert) rather than a bare error string.
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

  // Platform-readiness states. None of these render a tab strip, so
  // waitForOpen/waitForOpenRestricted do not apply — assert on the heading
  // text directly (EmptyState renders it as a plain div, no heading role).
  notConnectedHeading(): Locator {
    return this.page.getByText('Connect this tenant to erunpaas.com', { exact: true });
  }

  connectApiUrlInput(): Locator {
    return this.page.getByLabel('Platform API URL');
  }

  connectButton(): Locator {
    return this.page.getByRole('button', { name: 'Connect', exact: true });
  }

  connectErrorAlert(): Locator {
    return this.page.getByRole('alert');
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

  // FieldLabel appends a visually-hidden "(required)" to the accessible
  // name, so this intentionally does not use exact matching.
  enrollUsernameInput(): Locator {
    return this.page.getByLabel('Username');
  }

  tryEnrollButton(): Locator {
    return this.page.getByRole('button', { name: 'Try to enroll myself' });
  }

  enrollAdminCommand(): Locator {
    return this.page.locator('#enroll-admin-command');
  }

  enrollErrorAlert(): Locator {
    return this.page.getByRole('alert');
  }

  noPermissionHeading(): Locator {
    return this.page.getByText("You do not have access to this tenant's dashboard", {
      exact: true,
    });
  }

  platformContactLine(): Locator {
    return this.page.getByText('Platform:', { exact: false });
  }

  // Registration tab: the tenant/environment registration path
  // `erun platform` gives the CLI, surfaced in the desktop.
  twoRegistriesNotice(): Locator {
    return this.activePanel().getByText('live in this machine', { exact: false });
  }

  contextsHeading(): Locator {
    return this.activePanel().getByRole('heading', { name: 'Cloud contexts' });
  }

  contextsEmptyState(): Locator {
    return this.activePanel().getByText('No cloud contexts registered', { exact: true });
  }

  // FieldLabel appends a visually-hidden "(required)" to the accessible
  // name, so this intentionally does not use exact matching; .first() picks
  // the context form's Name field over the environment register form's.
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

  // The environment form: one field set backs both Preview and Register.
  // .last() picks it over the cloud-context form's own "Name" field.
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

  deployButtonFor(name: string): Locator {
    return this.environmentRow(name).getByRole('button', { name: 'Deploy' });
  }

  stopButtonFor(name: string): Locator {
    return this.environmentRow(name).getByRole('button', { name: 'Stop' });
  }

  // deleteButtonFor is the same control before and after the confirmation
  // step starts (DeleteControl keeps the label "Delete" for both — only the
  // surrounding row changes), so one locator serves the initial click and
  // the confirmed one.
  deleteButtonFor(name: string): Locator {
    return this.environmentRow(name).getByRole('button', { name: 'Delete', exact: true });
  }

  deleteConfirmInputFor(name: string): Locator {
    return this.environmentRow(name).getByRole('textbox');
  }

  deleteCancelButtonFor(name: string): Locator {
    return this.environmentRow(name).getByRole('button', { name: 'Cancel' });
  }

  // recoverableNote is the "expected, not broken" outcome — a quota cap or a
  // deploy already in flight — rendered role="status", never role="alert".
  recoverableNote(text: string): Locator {
    return this.activePanel().getByText(text, { exact: false });
  }

  // RequestInvitationDialog: the "Request an invitation" action off
  // NotEnrolledState. The dialog's accessible name comes from
  // aria-labelledby -> DialogTitle "Request an invitation".
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
    // exact: true -- the dialog's own description sentence also contains the
    // substring "Your verified identity" ("... Your verified identity goes
    // with the request"), so a non-exact match is ambiguous between the two.
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

  // The MyInviteRequestError branch: a transport failure reading the
  // caller's own invite request must never render as "never requested" --
  // see tenant_platform_invite_requests.go's loadTenantDashboardMyInviteRequest.
  // No `name` filter: InlineAlert's role="alert" element has no aria-label,
  // and ARIA's accname algorithm does not compute "name from content" for
  // the alert role, so a role+name locator would never match it.
  requestStatusCheckFailedAlert(): Locator {
    return this.page.getByRole('alert').filter({ hasText: 'invitation request status' });
  }

  requestStatusCheckRetryButton(): Locator {
    return this.page.getByRole('button', { name: 'Try again' });
  }

  // Requests tab: the operator/admin queue.
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
