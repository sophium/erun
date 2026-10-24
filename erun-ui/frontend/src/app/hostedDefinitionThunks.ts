import { environmentApi } from './api/environmentApi';
import type { HostedDefinitionUploadedPayload } from './model';
import { patchManageDialog } from './slices/manageDialogSlice';
import type { AppThunk } from './store';

// handleHostedDefinitionUploaded refreshes the Manage dialog's marker, which the
// environments-changed reload does not touch.
export const handleHostedDefinitionUploaded =
  (payload: HostedDefinitionUploadedPayload): AppThunk<Promise<void>> =>
  async (dispatch, getState) => {
    const tenant = payload.tenant.trim();
    const environment = payload.environment.trim();
    if (!tenant || !environment) {
      return;
    }
    const dialog = getState().manageDialog;
    if (
      !dialog.open ||
      dialog.selection?.tenant !== tenant ||
      dialog.selection.environment !== environment
    ) {
      return;
    }
    const request = dispatch(
      environmentApi.endpoints.getEnvironmentConfig.initiate(
        { tenant, environment },
        { forceRefetch: true },
      ),
    );
    try {
      const refreshed = await request.unwrap();
      // Write back only the marker so unsaved edits survive; re-read state because the
      // dialog may have closed during the await.
      const current = getState().manageDialog;
      dispatch(
        patchManageDialog({
          config: { ...current.config, hosted: refreshed.hosted },
          ...(current.initialConfig
            ? { initialConfig: { ...current.initialConfig, hosted: refreshed.hosted } }
            : {}),
        }),
      );
    } catch (error) {
      console.error('handleHostedDefinitionUploaded failed:', error);
    } finally {
      request.unsubscribe();
    }
  };
