// tenantEnrollmentPoll.ts backs the sidebar's per-tenant platform-enrollment
// status icon (Sidebar.TenantEnrollmentStatus.tsx): resolving each eligible
// tenant's local-only/pending/declined/enrolled state, and noticing an
// approval that happened while the desktop wasn't looking so it can surface
// a detached notification and a durable Activity Queue entry without the
// operator reopening anything.
//
// Each eligible tenant's own poll is entirely self-contained -- one hook
// call per tenant row, querying ListTenantPlatformEnrollmentStatuses with a
// singleton {tenants: [tenant]} list rather than one call shared across every
// tenant row. A tenant that has nothing left to observe (declined and
// enrolled are both stable until an operator or the requester acts again --
// see the state table below) never keeps a shared timer alive for siblings
// that still do.
//
// Polling gate: 'local-only' is never polled -- not because nothing can move
// it (signing in with an existing account is exactly a local-only -> enrolled
// transition, and the popover built on this state offers that sign-in), but
// because the transition is caused by a discrete, observable desktop action
// rather than something to notice by re-asking on a timer. cloudProviderThunks
// invalidates the 'TenantEnrollment' tag on a successful platform sign-in, and
// tenantDialogThunks does the same after the tenant dashboard's own load
// resolves a whoami -- both cheaper and more immediate than a per-row timer
// for a state most tenants in a large sidebar are legitimately local-only
// forever. 'pending'/'declined' ARE polled (both can change via an operator
// action elsewhere); 'enrolled' stops polling for that tenant permanently --
// this state changes at most a few times in a tenant's life and then never
// again. 'unknown' is also polled: it means the platform round trip itself
// failed (not "nothing pending"), so the only way to recover is to keep
// trying rather than going silent forever.
//
// There is no backend-supplied interval for this the way GET /v1/config
// threads other values, and a hard-coded frontend interval has previously
// raced a production timer in a Playwright spec, so the mitigation here is
// the same one environment_activity.go's TriggerEnvironmentActivitySweep
// uses for its own backend sweep loop: a synchronous test-trigger a spec can
// call instead of waiting on the timer. There is no separate backend sweep
// loop behind this poll (it is plain frontend RTK Query polling), so the
// equivalent hook is simply dispatching the same query endpoint's own forced
// refetch -- see Sidebar.TenantEnrollmentStatus.tsx's playwright spec for
// exactly that.

import * as React from 'react';

import { HOSTED_PLATFORM_API_URL } from '@/app/hostedPlatform';
import {
  TENANT_ENROLLMENT_DECLINED,
  TENANT_ENROLLMENT_ENROLLED,
  TENANT_ENROLLMENT_PENDING,
  TENANT_ENROLLMENT_UNKNOWN,
  type UITenantPlatformEnrollmentStatus,
} from '@/types';

import { pushInviteApprovalActivityEntry } from './activityQueueState';
import {
  tenantInviteRequestApi,
  useListTenantPlatformEnrollmentStatusesQuery,
} from './api/tenantInviteRequestApi';
import { useAppDispatch } from './hooks';
import { showNotification } from './notificationThunks';
import type { AppDispatch } from './store';

export const TENANT_ENROLLMENT_POLL_INTERVAL_MS = 30_000;

function isNonTerminalEnrollmentState(state: string): boolean {
  return (
    state === TENANT_ENROLLMENT_PENDING ||
    state === TENANT_ENROLLMENT_DECLINED ||
    state === TENANT_ENROLLMENT_UNKNOWN
  );
}

// buildInviteAcceptLink mirrors erun-console's InvitesPanel.acceptURL route
// (/accept-invite?token=...) against the one hosted platform's console
// origin. api.<domain>/console.<domain> are documented as the same apex
// host's two subdomains (erun-console/AGENTS.md's "Module role"), and
// HOSTED_PLATFORM_API_URL already names that one hosted platform elsewhere in
// this module (TenantPlatformState.tsx's own connect-URL prefill) -- so
// swapping the leading label is a safe derivation for it, not a guess about
// an arbitrary tenant's own platform alias, which this poll has no way to
// resolve a console origin for. Returns undefined rather than a wrong URL
// when the constant doesn't match the assumed shape.
export function buildInviteAcceptLink(token: string): string | undefined {
  const match = /^(https?:\/\/)api\.(.+)$/.exec(HOSTED_PLATFORM_API_URL);
  const scheme = match?.[1];
  const rest = match?.[2];
  if (!scheme || !rest) {
    return undefined;
  }
  return `${scheme}console.${rest}/accept-invite?token=${encodeURIComponent(token)}`;
}

// tenantEnrollmentApprovalMessage is the one sentence both the notification
// and the Activity Queue entry show for the same event, kept in one place so
// the two surfaces cannot drift.
export function tenantEnrollmentApprovalMessage(tenant: string): string {
  return `Approved -- you're enrolled in ${tenant}.`;
}

// nextEnrollmentPollingInterval and enrollmentApproved are split out as pure
// functions so the transition logic is unit-testable without mounting a
// component.
export function nextEnrollmentPollingInterval(state: string | undefined): number {
  return state && isNonTerminalEnrollmentState(state) ? TENANT_ENROLLMENT_POLL_INTERVAL_MS : 0;
}

// enrollmentApproved reports whether `previous -> current` is the one
// transition worth notifying about: leaving a non-terminal state (pending,
// declined -- a declined request can be retried and later approved, or
// unknown -- a prior failed round trip) and landing on enrolled.
export function enrollmentApproved(previous: string | undefined, current: string): boolean {
  if (previous === undefined || !isNonTerminalEnrollmentState(previous)) {
    return false;
  }
  return current === TENANT_ENROLLMENT_ENROLLED;
}

// useTenantEnrollmentStatus resolves one tenant's enrollment state and drives
// the poll described in this file's header comment.
export function useTenantEnrollmentStatus(
  tenant: string,
): UITenantPlatformEnrollmentStatus | undefined {
  const dispatch = useAppDispatch();
  const [pollingInterval, setPollingInterval] = React.useState(0);
  const previousState = React.useRef<string | undefined>(undefined);
  const { data } = useListTenantPlatformEnrollmentStatusesQuery(
    { tenants: [tenant] },
    { pollingInterval },
  );
  const status = data?.[0];

  React.useEffect(() => {
    if (!status) {
      return;
    }
    if (enrollmentApproved(previousState.current, status.state)) {
      notifyTenantEnrollmentApproved(dispatch, tenant);
    }
    previousState.current = status.state;
    setPollingInterval(nextEnrollmentPollingInterval(status.state));
  }, [dispatch, status, tenant]);

  return status;
}

function notifyTenantEnrollmentApproved(dispatch: AppDispatch, tenant: string): void {
  const message = tenantEnrollmentApprovalMessage(tenant);
  dispatch(showNotification('success', message, { tenant, action: 'invite-approved' }));
  void dispatch(
    tenantInviteRequestApi.endpoints.getMyTenantInviteRequest.initiate(
      { tenant },
      { forceRefetch: true },
    ),
  )
    .unwrap()
    .then((request) => {
      const inviteLink = request?.mintedInviteToken
        ? buildInviteAcceptLink(request.mintedInviteToken)
        : undefined;
      dispatch(pushInviteApprovalActivityEntry({ tenant, message, inviteLink }));
    })
    .catch(() => {
      // The invite link is a nice-to-have fallback artefact, not the notice
      // itself -- a failed re-fetch of the caller's own request still gets
      // the Activity Queue entry, just without a link to copy.
      dispatch(pushInviteApprovalActivityEntry({ tenant, message }));
    });
}
