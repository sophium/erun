// tenantInviteRequestThunks drives "Request an invitation" and the queue's issue/decline actions.

import {
  INVITE_REQUEST_KIND_JOIN_TENANT,
  type UIDeclineInviteRequestInput,
  type UISubmitInviteRequestInput,
} from '@/types';

import { tenantInviteRequestApi } from './api/tenantInviteRequestApi';
import { readError } from './errors';
import { useAppSelector } from './hooks';
import { showNotification } from './notificationThunks';
import { patchTenantDashboard } from './slices/tenantDashboardSlice';
import type { AppThunk } from './store';
import { loadTenantDashboard } from './tenantDialogThunks';

function trimmedOrUndefined(value: string | undefined): string | undefined {
  const trimmed = value?.trim();
  return trimmed === '' ? undefined : trimmed;
}

export const openRequestInvitationDialog = (): AppThunk => (dispatch) => {
  dispatch(patchTenantDashboard({ requestDialogOpen: true, requestError: '' }));
};

export const closeRequestInvitationDialog = (): AppThunk => (dispatch, getState) => {
  if (getState().tenantDashboard.requesting) {
    return;
  }
  dispatch(patchTenantDashboard({ requestDialogOpen: false, requestError: '' }));
};

export const setRequestKindDraft =
  (kind: string): AppThunk =>
  (dispatch) => {
    dispatch(patchTenantDashboard({ requestKindDraft: kind }));
  };

export const setRequestNoteDraft =
  (note: string): AppThunk =>
  (dispatch) => {
    dispatch(patchTenantDashboard({ requestNoteDraft: note }));
  };

export function requestInvitationSubmitDisabledReason(
  requesting: boolean,
  rateLimitedUntil: number,
  now: number,
): string {
  if (requesting) {
    return '';
  }
  const remainingMs = rateLimitedUntil - now;
  if (remainingMs <= 0) {
    return '';
  }
  const seconds = Math.ceil(remainingMs / 1000);
  return seconds === 1
    ? 'You can send another request in 1 second.'
    : `You can send another request in ${String(seconds)} seconds.`;
}

// The requester's identity is never sent; the platform reads it from the bearer.
export const submitInviteRequest =
  (params: { tenantName: string; environmentName?: string }): AppThunk<Promise<void>> =>
  async (dispatch, getState) => {
    const dashboard = getState().tenantDashboard;
    if (dashboard.requesting) {
      return;
    }
    const tenantName = params.tenantName.trim();
    if (!tenantName) {
      return;
    }
    dispatch(patchTenantDashboard({ requesting: true, requestError: '' }));
    const input: UISubmitInviteRequestInput = {
      tenant: dashboard.tenant,
      kind: dashboard.requestKindDraft || INVITE_REQUEST_KIND_JOIN_TENANT,
      tenantName,
      environmentName: trimmedOrUndefined(params.environmentName),
      note: trimmedOrUndefined(dashboard.requestNoteDraft),
    };
    try {
      const outcome = await dispatch(
        tenantInviteRequestApi.endpoints.submitTenantInviteRequest.initiate(input),
      ).unwrap();
      if (outcome.rateLimited) {
        dispatch(
          patchTenantDashboard({
            requesting: false,
            requestError: '',
            requestRateLimitedUntil: Date.now() + outcome.rateLimited.retryAfterSeconds * 1000,
          }),
        );
        return;
      }
      dispatch(
        patchTenantDashboard({
          requesting: false,
          requestError: '',
          requestDialogOpen: false,
          requestRateLimitedUntil: 0,
        }),
      );
      dispatch(showNotification('success', `Requested an invitation for ${tenantName}.`));
      await dispatch(loadTenantDashboard());
    } catch (error) {
      dispatch(patchTenantDashboard({ requesting: false, requestError: readError(error) }));
    }
  };

export const startDecliningInviteRequest =
  (inviteRequestId: string): AppThunk =>
  (dispatch) => {
    dispatch(
      patchTenantDashboard({
        decliningInviteRequestId: inviteRequestId,
        declineReasonDraft: '',
        decideInviteRequestError: '',
      }),
    );
  };

export const cancelDecliningInviteRequest = (): AppThunk => (dispatch, getState) => {
  if (getState().tenantDashboard.decidingInviteRequestId) {
    return;
  }
  dispatch(patchTenantDashboard({ decliningInviteRequestId: '', declineReasonDraft: '' }));
};

export const setDeclineReasonDraft =
  (reason: string): AppThunk =>
  (dispatch) => {
    dispatch(patchTenantDashboard({ declineReasonDraft: reason }));
  };

// A decline needs a reason; this backs up the component's disabled confirm.
export const confirmDeclineInviteRequest =
  (): AppThunk<Promise<void>> => async (dispatch, getState) => {
    const dashboard = getState().tenantDashboard;
    const reason = dashboard.declineReasonDraft.trim();
    const inviteRequestId = dashboard.decliningInviteRequestId;
    if (!reason || !inviteRequestId || dashboard.decidingInviteRequestId) {
      return;
    }
    dispatch(patchTenantDashboard({ decidingInviteRequestId: inviteRequestId }));
    const input: UIDeclineInviteRequestInput = {
      tenant: dashboard.tenant,
      inviteRequestId,
      reason,
    };
    try {
      await dispatch(
        tenantInviteRequestApi.endpoints.declineTenantInviteRequest.initiate(input),
      ).unwrap();
      dispatch(
        patchTenantDashboard({
          decidingInviteRequestId: '',
          decideInviteRequestError: '',
          decliningInviteRequestId: '',
          declineReasonDraft: '',
        }),
      );
      dispatch(showNotification('success', 'Request declined.'));
      await dispatch(loadTenantDashboard());
    } catch (error) {
      dispatch(
        patchTenantDashboard({
          decidingInviteRequestId: '',
          decideInviteRequestError: readError(error),
        }),
      );
    }
  };

export const approveInviteRequest =
  (inviteRequestId: string): AppThunk<Promise<void>> =>
  async (dispatch, getState) => {
    const dashboard = getState().tenantDashboard;
    if (dashboard.decidingInviteRequestId) {
      return;
    }
    dispatch(patchTenantDashboard({ decidingInviteRequestId: inviteRequestId }));
    try {
      const approved = await dispatch(
        tenantInviteRequestApi.endpoints.approveTenantInviteRequest.initiate({
          tenant: dashboard.tenant,
          inviteRequestId,
        }),
      ).unwrap();
      dispatch(
        patchTenantDashboard({
          decidingInviteRequestId: '',
          decideInviteRequestError: '',
          issuedInviteLink: approved.mintedInviteToken
            ? { inviteRequestId, token: approved.mintedInviteToken }
            : null,
        }),
      );
      dispatch(showNotification('success', `Invitation issued for ${approved.tenantName}.`));
      await dispatch(loadTenantDashboard());
    } catch (error) {
      dispatch(
        patchTenantDashboard({
          decidingInviteRequestId: '',
          decideInviteRequestError: readError(error),
        }),
      );
    }
  };

export function useMyInviteRequest() {
  return useAppSelector((state) => state.tenantDashboard.data?.myInviteRequest ?? null);
}
