import { HOSTED_PLATFORM_API_URL } from '@/app/hostedPlatform';
import type { TenantDashboardState } from '@/app/state';

import { tenantApi } from './api/tenantApi';
import { replaceCloudProvider } from './cloudContextState';
import { type CloudProviderUpdateOutcome, signInAndRecover } from './cloudProviderThunks';
import { readError } from './errors';
import { patchTenantDashboard } from './slices/tenantDashboardSlice';
import { setCloudProviders } from './slices/tenantsSlice';
import type { AppThunk } from './store';
import { loadTenantDashboard } from './tenantDialogThunks';

export const setConnectApiUrlDraft =
  (value: string): AppThunk =>
  (dispatch) => {
    dispatch(patchTenantDashboard({ connectApiUrlDraft: value }));
  };

// connectTenantPlatform attaches apiUrl as the tenant's cloud alias and signs
// in, since InitERunCloudProvider does not sign in on its own. The tenant is
// sent with the attach so the alias lands on the tenant whose card was clicked.
export const connectTenantPlatform =
  (apiUrl: string): AppThunk<Promise<void>> =>
  async (dispatch, getState) => {
    const trimmed = apiUrl.trim();
    if (!trimmed || getState().tenantDashboard.connecting) {
      return;
    }
    dispatch(patchTenantDashboard({ connecting: true, connectError: '' }));
    try {
      const tenant = getState().tenantDashboard.tenant.trim();
      const provider = await dispatch(
        tenantApi.endpoints.connectERunPlatform.initiate(
          tenant ? { apiUrl: trimmed, tenant } : { apiUrl: trimmed },
        ),
      ).unwrap();
      dispatch(
        setCloudProviders(replaceCloudProvider(getState().tenants.cloudProviders, provider)),
      );
      // connecting stays set until the sign-in settles, so Connect is not
      // re-enabled while the sign-in this click started is in flight.
      const outcome = await dispatch(
        signInAndRecover(provider.alias, () => {
          void dispatch(loadTenantDashboard());
        }),
      );
      dispatch(
        patchTenantDashboard({
          connecting: false,
          connectError: signInOutcomeMessage(outcome, provider.alias),
        }),
      );
    } catch (error) {
      dispatch(
        patchTenantDashboard({
          connecting: false,
          connectError: connectFailureMessage(error, trimmed),
        }),
      );
    }
  };

// signInOutcomeMessage explains a skipped or failed sign-in; otherwise the card
// looks unchanged after a successful attach.
function signInOutcomeMessage(outcome: CloudProviderUpdateOutcome, alias: string): string {
  switch (outcome.status) {
    case 'success':
      return '';
    case 'skipped':
      return `${alias} was already signing in from another action, so this sign-in did not start. Wait for that one to finish, then click Connect again.`;
    case 'failed':
      return `The alias ${alias} was attached, but signing in failed: ${outcome.message} Click Connect again to retry the sign-in.`;
  }
}

function connectFailureMessage(error: unknown, attempted: string): string {
  const message = readError(error);
  if (attempted === HOSTED_PLATFORM_API_URL) {
    return message;
  }
  return `${message} The hosted erun platform is normally reachable at ${HOSTED_PLATFORM_API_URL}.`;
}

export const setEnrollUsernameDraft =
  (value: string): AppThunk =>
  (dispatch) => {
    dispatch(patchTenantDashboard({ enrollUsernameDraft: value }));
  };

interface EnrollFields {
  alias: string;
  issuer: string;
  subject: string;
  username: string;
}

function trimmedField(value: string | undefined): string {
  return value?.trim() ?? '';
}

function enrollInput(dashboard: TenantDashboardState): EnrollFields | null {
  const data = dashboard.data;
  const fields: EnrollFields = {
    alias: trimmedField(data?.platformAlias),
    issuer: trimmedField(data?.platformIssuer),
    subject: trimmedField(data?.platformSubject),
    username: dashboard.enrollUsernameDraft.trim(),
  };
  return Object.values(fields).every((value) => value !== '') ? fields : null;
}

// enrollTenantPlatformUser succeeds only for a tenant with no users yet or a
// caller with user-management capability; the platform refuses everyone else.
export const enrollTenantPlatformUser =
  (): AppThunk<Promise<void>> => async (dispatch, getState) => {
    const dashboard = getState().tenantDashboard;
    const input = enrollInput(dashboard);
    if (!input || dashboard.enrolling) {
      return;
    }
    dispatch(patchTenantDashboard({ enrolling: true, enrollError: '' }));
    try {
      await dispatch(tenantApi.endpoints.enrollERunPlatformUser.initiate(input)).unwrap();
      dispatch(patchTenantDashboard({ enrolling: false, enrollError: '' }));
      await dispatch(loadTenantDashboard());
    } catch (error) {
      dispatch(patchTenantDashboard({ enrolling: false, enrollError: readError(error) }));
    }
  };
