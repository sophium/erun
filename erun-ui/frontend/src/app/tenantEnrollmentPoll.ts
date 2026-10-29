// Backs the sidebar's per-tenant enrollment icon: one poll per tenant row, and
// notification when an approval lands. 'local-only' and 'tenant-mismatch' are
// not polled (sign-in invalidates the TenantEnrollment tag); 'enrolled' stops
// polling; 'unknown' keeps retrying.

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

// buildInviteAcceptLink derives the console origin from HOSTED_PLATFORM_API_URL
// (api.<domain> -> console.<domain>); undefined when it doesn't match that shape.
export function buildInviteAcceptLink(token: string): string | undefined {
  const match = /^(https?:\/\/)api\.(.+)$/.exec(HOSTED_PLATFORM_API_URL);
  const scheme = match?.[1];
  const rest = match?.[2];
  if (!scheme || !rest) {
    return undefined;
  }
  return `${scheme}console.${rest}/accept-invite?token=${encodeURIComponent(token)}`;
}

export function tenantEnrollmentApprovalMessage(tenant: string): string {
  return `Approved -- you're enrolled in ${tenant}.`;
}

export function nextEnrollmentPollingInterval(state: string | undefined): number {
  return state && isNonTerminalEnrollmentState(state) ? TENANT_ENROLLMENT_POLL_INTERVAL_MS : 0;
}

// enrollmentApproved is true when leaving pending, declined or unknown for enrolled.
export function enrollmentApproved(previous: string | undefined, current: string): boolean {
  if (previous === undefined || !isNonTerminalEnrollmentState(previous)) {
    return false;
  }
  return current === TENANT_ENROLLMENT_ENROLLED;
}

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
      // A failed link re-fetch still gets the Activity Queue entry.
      dispatch(pushInviteApprovalActivityEntry({ tenant, message }));
    });
}
