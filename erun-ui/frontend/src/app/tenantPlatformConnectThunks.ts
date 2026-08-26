// tenantPlatformConnectThunks drives the tenant dashboard's not-connected and
// not-enrolled states: connecting a tenant to a hosted erun platform for the
// first time, and enrolling the signed-in identity into it. Split out of
// tenantDialogThunks.ts to keep that file under eslint's 500-line cap.

import type { TenantDashboardState } from '@/app/state';

import { tenantApi } from './api/tenantApi';
import { replaceCloudProvider } from './cloudContextState';
import { signInAndRecover } from './cloudProviderThunks';
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

// connectTenantPlatform attaches apiUrl as this machine's erun-type cloud
// alias, then immediately signs into it and reloads the dashboard —
// InitERunCloudProvider performs no sign-in on its own, so chaining straight
// into it is what makes this a single click from "not connected" to
// "working" (Smooth: no dead ends between discrete steps the operator would
// otherwise have to notice and trigger themselves).
export const connectTenantPlatform =
  (apiUrl: string): AppThunk<Promise<void>> =>
  async (dispatch, getState) => {
    const trimmed = apiUrl.trim();
    if (!trimmed || getState().tenantDashboard.connecting) {
      return;
    }
    dispatch(patchTenantDashboard({ connecting: true, connectError: '' }));
    try {
      const provider = await dispatch(
        tenantApi.endpoints.connectERunPlatform.initiate({ apiUrl: trimmed }),
      ).unwrap();
      dispatch(
        setCloudProviders(replaceCloudProvider(getState().tenants.cloudProviders, provider)),
      );
      dispatch(patchTenantDashboard({ connecting: false, connectError: '' }));
      await dispatch(
        signInAndRecover(provider.alias, () => {
          void dispatch(loadTenantDashboard());
        }),
      );
    } catch (error) {
      dispatch(patchTenantDashboard({ connecting: false, connectError: readError(error) }));
    }
  };

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
