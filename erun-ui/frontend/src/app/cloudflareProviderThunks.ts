import type { UICloudflareCloudAliasInput } from '@/types';

import { cloudApi } from './api/cloudApi';
import { replaceCloudProvider } from './cloudContextState';
import { readError } from './errors';
import { showNotification, showTerminalMessage } from './notificationThunks';
import { patchGlobalConfigDialog } from './slices/globalConfigDialogSlice';
import { defaultCloudflareCloudAliasInput } from './state';
import type { AppThunk } from './store';

// cloudflareProviderThunks own the global-config dialog's non-interactive "add
// Cloudflare token" flow. Unlike AWS (an SSO PTY session that takes over the
// terminal), Cloudflare add is a masked form rendered inside the settings
// dialog: collect account ID + token label + scoped token, verify the token,
// store it off-config, and add the alias — no terminal.

export const openCloudflareCloudInitForm = (): AppThunk => (dispatch, getState) => {
  const dialog = getState().globalConfigDialog;
  if (dialog.busy || dialog.configLoading) {
    return;
  }
  dispatch(
    patchGlobalConfigDialog({
      cloudflareFormOpen: true,
      cloudflareDraft: defaultCloudflareCloudAliasInput(),
      error: '',
    }),
  );
};

export const closeCloudflareCloudInitForm = (): AppThunk => (dispatch, getState) => {
  if (getState().globalConfigDialog.busy) {
    return;
  }
  dispatch(
    patchGlobalConfigDialog({
      cloudflareFormOpen: false,
      cloudflareDraft: defaultCloudflareCloudAliasInput(),
      error: '',
    }),
  );
};

export const updateCloudflareDraft =
  (values: Partial<UICloudflareCloudAliasInput>): AppThunk =>
  (dispatch, getState) => {
    const dialog = getState().globalConfigDialog;
    if (dialog.busy || dialog.configLoading) {
      return;
    }
    dispatch(
      patchGlobalConfigDialog({
        cloudflareDraft: { ...dialog.cloudflareDraft, ...values },
        error: '',
      }),
    );
  };

// submitCloudflareCloudInit verifies the scoped token against Cloudflare,
// stores it off-config, and adds the alias. On success the form closes, the
// alias is merged into the dialog's provider list (so the new row appears with
// its live status), and the masked token is dropped from client state.
export const submitCloudflareCloudInit =
  (): AppThunk<Promise<void>> => async (dispatch, getState) => {
    const dialog = getState().globalConfigDialog;
    if (dialog.busy || dialog.configLoading) {
      return;
    }
    dispatch(
      patchGlobalConfigDialog({
        busy: true,
        busyAction: 'cloud-provider-cloudflare-init',
        busyTarget: '',
        error: '',
      }),
    );
    try {
      const provider = await dispatch(
        cloudApi.endpoints.initCloudflareCloudProvider.initiate(dialog.cloudflareDraft),
      ).unwrap();
      const currentConfig = getState().globalConfigDialog.config;
      dispatch(
        patchGlobalConfigDialog({
          config: {
            ...currentConfig,
            cloudProviders: replaceCloudProvider(currentConfig.cloudProviders ?? [], provider),
          },
          cloudflareFormOpen: false,
          cloudflareDraft: defaultCloudflareCloudAliasInput(),
          busy: false,
          busyAction: '',
          busyTarget: '',
          error: '',
        }),
      );
      dispatch(showNotification('success', `Added Cloudflare token ${provider.alias}.`));
    } catch (error) {
      const message = readError(error);
      dispatch(
        patchGlobalConfigDialog({
          busy: false,
          busyAction: '',
          busyTarget: '',
          error: message,
        }),
      );
      dispatch(showTerminalMessage(message));
    }
  };
