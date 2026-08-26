import type { UICloudProviderStatus } from '@/types';

import { ClipboardSetText } from '../../wailsjs/runtime/runtime';
import { cloudApi } from './api/cloudApi';
import { replaceCloudProvider } from './cloudContextState';
import { readError } from './errors';
import { showNotification, showTerminalError, showTerminalMessage } from './notificationThunks';
import { setSidebarCloudAliasBusy } from './slices/sidebarSlice';
import { setCloudProviders } from './slices/tenantsSlice';
import type { AppThunk, RootState } from './store';

// Per-alias busy/action state keeps AWS and Cloudflare sidebar rows spinning independently.

// loginPrimaryCloudProvider reports whether the sign-in actually succeeded so
// a caller that needs to react to success (e.g. PlatformErrorAlert's sign-in
// action re-fetching what failed) can tell that apart from a swallowed error.
export const loginPrimaryCloudProvider =
  (alias: string): AppThunk<Promise<boolean>> =>
  async (dispatch) =>
    dispatch(
      updatePrimaryCloudProvider(alias, 'login', (target) =>
        dispatch(cloudApi.endpoints.loginCloudProvider.initiate(target)).unwrap(),
      ),
    );

// signInAndRecover is PlatformErrorAlert's own "Log in" thunk (#1392): sign
// in, and only once the sign-in itself reports success, run the caller's
// recovery — re-fetching whatever produced the identity error, or clearing a
// stale write error so the operator can retry it. A failed sign-in runs no
// recovery at all, leaving the alert to report the failure itself rather
// than silently re-rendering the same message and button. Deliberately not
// added to loginPrimaryCloudProvider itself: that thunk's original caller is
// the sidebar's own login button, which has no dashboard or dialog to
// refresh as a side effect of logging in.
export const signInAndRecover =
  (alias: string, recover: () => void): AppThunk<Promise<boolean>> =>
  async (dispatch) => {
    const signedIn = await dispatch(loginPrimaryCloudProvider(alias));
    if (signedIn) {
      recover();
    }
    return signedIn;
  };

export const logoutPrimaryCloudProvider =
  (alias: string): AppThunk<Promise<void>> =>
  async (dispatch) => {
    await dispatch(
      updatePrimaryCloudProvider(alias, 'logout', (target) =>
        dispatch(cloudApi.endpoints.logoutCloudProvider.initiate(target)).unwrap(),
      ),
    );
  };

export const getPrimaryCloudProviderBearerToken =
  (alias: string): AppThunk<Promise<void>> =>
  async (dispatch, getState) => {
    alias = alias.trim();
    if (!alias || aliasBusy(getState, alias)) {
      return;
    }
    dispatch(setSidebarCloudAliasBusy({ alias, busy: true, action: 'bearer' }));
    try {
      const result = await dispatch(
        cloudApi.endpoints.getCloudProviderBearerToken.initiate(alias),
      ).unwrap();
      await ClipboardSetText(result.token);
      dispatch(
        setCloudProviders(replaceCloudProvider(getState().tenants.cloudProviders, result.provider)),
      );
      dispatch(setSidebarCloudAliasBusy({ alias, busy: false, action: '' }));
      const issuer = result.issuer?.trim();
      dispatch(
        showTerminalMessage(
          issuer
            ? `Copied bearer token for ${result.alias}. Issuer: ${issuer}`
            : `Copied bearer token for ${result.alias}.`,
        ),
      );
      dispatch(showNotification('success', `Copied bearer token for ${result.alias}.`));
    } catch (error) {
      const message = readError(error);
      dispatch(setSidebarCloudAliasBusy({ alias, busy: false, action: '' }));
      dispatch(showTerminalError(message));
      dispatch(showNotification('error', message));
    }
  };

function aliasBusy(getState: () => RootState, alias: string): boolean {
  return Boolean(getState().sidebar.sidebarCloudAliasBusyByAlias[alias]);
}

const updatePrimaryCloudProvider =
  (
    alias: string,
    action: 'login' | 'logout',
    run: (alias: string) => Promise<unknown>,
  ): AppThunk<Promise<boolean>> =>
  async (dispatch, getState) => {
    alias = alias.trim();
    if (!alias || aliasBusy(getState, alias)) {
      return false;
    }
    dispatch(setSidebarCloudAliasBusy({ alias, busy: true, action }));
    try {
      const provider = (await run(alias)) as UICloudProviderStatus;
      dispatch(
        setCloudProviders(replaceCloudProvider(getState().tenants.cloudProviders, provider)),
      );
      dispatch(setSidebarCloudAliasBusy({ alias, busy: false, action: '' }));
      dispatch(showTerminalMessage(`${provider.alias}: ${provider.status}`));
      return true;
    } catch (error) {
      const message = readError(error);
      dispatch(setSidebarCloudAliasBusy({ alias, busy: false, action: '' }));
      dispatch(showTerminalError(message));
      dispatch(showNotification('error', message));
      return false;
    }
  };
