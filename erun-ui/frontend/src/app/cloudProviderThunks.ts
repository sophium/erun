import { ClipboardSetText } from '../../wailsjs/runtime/runtime';
import { cloudApi } from './api/cloudApi';
import { replaceCloudProvider } from './cloudContextState';
import { readError } from './errors';
import { showNotification, showTerminalMessage } from './notificationThunks';
import { setSidebarCloudAliasBusy } from './slices/sidebarSlice';
import { setCloudProviders } from './slices/tenantsSlice';
import type { AppThunk } from './store';
import type { UICloudProviderStatus } from '@/types';

// cloudProviderThunks own the sidebar's primary-cloud-alias controls. The
// busy/action flags it writes live on the sidebar slice; the thunks dispatch
// the matching slice actions directly.

export const loginPrimaryCloudProvider = (alias: string): AppThunk<Promise<void>> =>
  async (dispatch) => {
    await dispatch(
      updatePrimaryCloudProvider(alias, 'login', (target) =>
        dispatch(cloudApi.endpoints.loginCloudProvider.initiate(target)).unwrap(),
      ),
    );
  };

export const logoutPrimaryCloudProvider = (alias: string): AppThunk<Promise<void>> =>
  async (dispatch) => {
    await dispatch(
      updatePrimaryCloudProvider(alias, 'logout', (target) =>
        dispatch(cloudApi.endpoints.logoutCloudProvider.initiate(target)).unwrap(),
      ),
    );
  };

export const getPrimaryCloudProviderBearerToken = (alias: string): AppThunk<Promise<void>> =>
  async (dispatch, getState) => {
    alias = alias.trim();
    if (!alias || getState().sidebar.sidebarCloudAliasBusy) {
      return;
    }
    dispatch(setSidebarCloudAliasBusy({ busy: true, action: 'bearer' }));
    try {
      const result = await dispatch(
        cloudApi.endpoints.getCloudProviderBearerToken.initiate(alias),
      ).unwrap();
      await ClipboardSetText(result.token);
      dispatch(setCloudProviders(replaceCloudProvider(getState().tenants.cloudProviders, result.provider)));
      dispatch(setSidebarCloudAliasBusy({ busy: false, action: '' }));
      const issuer = result.issuer?.trim();
      dispatch(showTerminalMessage(issuer ? `Copied bearer token for ${result.alias}. Issuer: ${issuer}` : `Copied bearer token for ${result.alias}.`));
      dispatch(showNotification('success', `Copied bearer token for ${result.alias}.`));
    } catch (error) {
      const message = readError(error);
      dispatch(setSidebarCloudAliasBusy({ busy: false, action: '' }));
      dispatch(showTerminalMessage(message));
      dispatch(showNotification('error', message));
    }
  };

const updatePrimaryCloudProvider = (
  alias: string,
  action: 'login' | 'logout',
  run: (alias: string) => Promise<unknown>,
): AppThunk<Promise<void>> => async (dispatch, getState) => {
  alias = alias.trim();
  if (!alias || getState().sidebar.sidebarCloudAliasBusy) {
    return;
  }
  dispatch(setSidebarCloudAliasBusy({ busy: true, action }));
  try {
    const provider = (await run(alias)) as UICloudProviderStatus;
    dispatch(setCloudProviders(replaceCloudProvider(getState().tenants.cloudProviders, provider)));
    dispatch(setSidebarCloudAliasBusy({ busy: false, action: '' }));
    dispatch(showTerminalMessage(`${provider.alias}: ${provider.status}`));
  } catch (error) {
    const message = readError(error);
    dispatch(setSidebarCloudAliasBusy({ busy: false, action: '' }));
    dispatch(showTerminalMessage(message));
    dispatch(showNotification('error', message));
  }
};
