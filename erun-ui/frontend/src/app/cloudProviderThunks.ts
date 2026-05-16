import { ClipboardSetText } from '../../wailsjs/runtime/runtime';
import { cloudApi } from './api/cloudApi';
import { replaceCloudProvider } from './cloudContextState';
import { readError } from './errors';
import { showNotification, showTerminalMessage } from './notificationThunks';
import type { AppThunk } from './store';
import { requireController } from './thunkExtra';
import type { UICloudProviderStatus } from '@/types';

// cloudProviderThunks own the sidebar's primary-cloud-alias controls. The
// busy/action flags it writes live on the sidebar slice; the proxy routes the
// state assignments to the slice actions.

export const loginPrimaryCloudProvider = (alias: string): AppThunk<Promise<void>> =>
  async (dispatch, _getState, extra) => {
    await dispatch(
      updatePrimaryCloudProvider(alias, 'login', (target) =>
        dispatch(cloudApi.endpoints.loginCloudProvider.initiate(target)).unwrap(),
      ),
    );
    void requireController(extra);
  };

export const logoutPrimaryCloudProvider = (alias: string): AppThunk<Promise<void>> =>
  async (dispatch, _getState, extra) => {
    await dispatch(
      updatePrimaryCloudProvider(alias, 'logout', (target) =>
        dispatch(cloudApi.endpoints.logoutCloudProvider.initiate(target)).unwrap(),
      ),
    );
    void requireController(extra);
  };

export const getPrimaryCloudProviderBearerToken = (alias: string): AppThunk<Promise<void>> =>
  async (dispatch, _getState, extra) => {
    const controller = requireController(extra);
    alias = alias.trim();
    if (!alias || controller.state.sidebarCloudAliasBusy) {
      return;
    }
    controller.state.sidebarCloudAliasBusy = true;
    controller.state.sidebarCloudAliasAction = 'bearer';
    try {
      const result = await dispatch(
        cloudApi.endpoints.getCloudProviderBearerToken.initiate(alias),
      ).unwrap();
      await ClipboardSetText(result.token);
      controller.state.cloudProviders = replaceCloudProvider(controller.state.cloudProviders, result.provider);
      controller.state.sidebarCloudAliasBusy = false;
      controller.state.sidebarCloudAliasAction = '';
      const issuer = result.issuer?.trim();
      dispatch(showTerminalMessage(issuer ? `Copied bearer token for ${result.alias}. Issuer: ${issuer}` : `Copied bearer token for ${result.alias}.`));
      dispatch(showNotification('success', `Copied bearer token for ${result.alias}.`));
    } catch (error) {
      const message = readError(error);
      controller.state.sidebarCloudAliasBusy = false;
      controller.state.sidebarCloudAliasAction = '';
      dispatch(showTerminalMessage(message));
      dispatch(showNotification('error', message));
    }
  };

const updatePrimaryCloudProvider = (
  alias: string,
  action: 'login' | 'logout',
  run: (alias: string) => Promise<unknown>,
): AppThunk<Promise<void>> => async (dispatch, _getState, extra) => {
  const controller = requireController(extra);
  alias = alias.trim();
  if (!alias || controller.state.sidebarCloudAliasBusy) {
    return;
  }
  controller.state.sidebarCloudAliasBusy = true;
  controller.state.sidebarCloudAliasAction = action;
  try {
    const provider = (await run(alias)) as UICloudProviderStatus;
    controller.state.cloudProviders = replaceCloudProvider(controller.state.cloudProviders, provider);
    controller.state.sidebarCloudAliasBusy = false;
    controller.state.sidebarCloudAliasAction = '';
    dispatch(showTerminalMessage(`${provider.alias}: ${provider.status}`));
  } catch (error) {
    const message = readError(error);
    controller.state.sidebarCloudAliasBusy = false;
    controller.state.sidebarCloudAliasAction = '';
    dispatch(showTerminalMessage(message));
    dispatch(showNotification('error', message));
  }
};
