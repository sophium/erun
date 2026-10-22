import { environmentApi } from './api/environmentApi';
import type { HostedDefinitionUploadedPayload } from './model';
import { patchManageDialog } from './slices/manageDialogSlice';
import type { AppThunk } from './store';

// The hosted-definition domain's own event handlers, kept beside
// hostedDefinitionDrift.ts rather than in the shared wailsEventThunks module:
// they belong to one domain concept and nothing else in the desktop reads
// them, which is the same reason the marker's own Wails types live in their
// own file.

// handleHostedDefinitionUploaded refreshes one environment's marker after an
// upload wrote a new definition revision for it.
//
// The upload's own config write also fires environments-changed, but that tick
// reloads the sidebar and the tenant list, and the Manage dialog renders its
// marker from the dialog slice rather than from either. Without this the
// operator would keep reading the revision the environment had before the
// upload, in the one dialog whose subject is that revision.
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
      // Only the marker is written back, in both the live config and the
      // dirty-check baseline. Replacing the config wholesale -- what a plain
      // reload does -- would discard anything the operator has typed but not
      // saved, and an upload the operator did not ask for must never do that.
      // The dialog is re-read after the await: the operator may have closed it
      // or switched environments while the read was in flight.
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
      // Best-effort, like the env-change reload: the upload itself succeeded
      // and the marker is on disk, so the only loss is this panel staying a
      // revision behind until it is reopened. Log it rather than letting the
      // rejection disappear.
      console.error('handleHostedDefinitionUploaded failed:', error);
    } finally {
      request.unsubscribe();
    }
  };
