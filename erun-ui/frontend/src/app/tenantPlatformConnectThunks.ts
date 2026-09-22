// tenantPlatformConnectThunks drives the tenant dashboard's not-connected and
// not-enrolled states: connecting a tenant to a hosted erun platform for the
// first time, and enrolling the signed-in identity into it. Split out of
// tenantDialogThunks.ts to keep that file under eslint's 500-line cap.

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

// connectTenantPlatform attaches apiUrl as the dashboard's own tenant's
// erun-type cloud alias, then immediately signs into it and reloads the
// dashboard — InitERunCloudProvider performs no sign-in on its own, so
// chaining straight into it is what makes this a single click from "not
// connected" to "working" (Smooth: no dead ends between discrete steps the
// operator would otherwise have to notice and trigger themselves).
//
// The tenant is read from the dashboard state this thunk is dispatched from
// and sent with the attach: the dashboard's platform resolution reads the
// tenant's own alias selection whenever that selection is non-empty, so an
// alias the attach left machine-global only cannot move the tenant whose
// Connect card was clicked.
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
      // connecting stays set across the sign-in: the click's outcome is not
      // known until the grant settles, and clearing it here would show a
      // re-enabled Connect button (and the same card) while the sign-in this
      // click started is still the thing that has to finish.
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

// signInOutcomeMessage is what the card says when the sign-in half of Connect
// did not finish. Without it a failed or skipped grant set no error at all
// and left the card byte-identical to the one before the click — the state
// the operator reads as "the button does nothing" even though the alias
// attach behind it succeeded. The two non-success outcomes are kept apart
// because their remedies differ: nothing was attempted for a skipped grant,
// while a failed one carries the grant's own reason.
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

// connectFailureMessage names the standard host beside a verification
// failure — unless the operator already tried it — so a mistyped or
// self-hosted URL that does not resolve is recoverable inline, without
// leaving the panel to go rediscover the right value.
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

// enrollInput resolves the four fields EnrollERunPlatformUser needs from
// dashboard state, or null when any is missing — split out so
// enrollTenantPlatformUser's own branching stays under the module's
// complexity cap.
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

// enrollTenantPlatformUser attempts to enroll the signed-in identity
// directly. This only succeeds for a brand-new tenant with no users yet
// (the platform's own first-user bootstrap) or when the caller already
// holds user-management capability — the platform's auth layer refuses every
// other protected route, this one included, for an identity it does not yet
// recognize. The common case is the caller showing the administrator
// hand-off instead; this stays a cheap, honest "try anyway" beside it.
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
