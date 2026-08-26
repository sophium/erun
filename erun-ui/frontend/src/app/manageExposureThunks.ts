import type { ExposeServiceFormState, UIExposeServiceInput } from '@/types';

import { environmentApi } from './api/environmentApi';
import { readError } from './errors';
import { patchManageDialog } from './slices/manageDialogSlice';
import type { AppThunk } from './store';

// refreshManageExposures loads the Ports tab's public-exposure list. Called
// automatically when the Manage dialog opens (matching deployComponents) and
// again after a successful expose/unexpose, so the list always reflects the
// cluster's actual Ingresses rather than an optimistic local edit.
export const refreshManageExposures = (): AppThunk<Promise<void>> => async (dispatch, getState) => {
  const dialog = getState().manageDialog;
  const selection = dialog.selection;
  if (!dialog.open || !selection) {
    return;
  }
  dispatch(patchManageDialog({ exposuresLoading: true }));
  try {
    const result = await dispatch(
      environmentApi.endpoints.listEnvironmentExposures.initiate(selection),
    ).unwrap();
    if (!getState().manageDialog.open) {
      return;
    }
    dispatch(patchManageDialog({ exposures: result, exposuresLoading: false }));
  } catch (error) {
    if (!getState().manageDialog.open) {
      return;
    }
    // A round-trip failure (not a computed restricted/unconfigured result) is
    // reported the same way a genuine listing failure is, so it renders with
    // the same "failed to load" affordance rather than reading as "nothing
    // exposed here".
    dispatch(
      patchManageDialog({
        exposuresLoading: false,
        exposures: { configured: true, restricted: false, error: readError(error), services: [] },
      }),
    );
  }
};

export const updateExposeForm =
  (values: Partial<ExposeServiceFormState>): AppThunk =>
  (dispatch, getState) => {
    const dialog = getState().manageDialog;
    if (dialog.exposeBusy) {
      return;
    }
    dispatch(
      patchManageDialog({ exposeForm: { ...dialog.exposeForm, ...values }, exposeError: '' }),
    );
  };

// submitExposeService exposes the form's service at a public hostname, then
// re-reads the list from the cluster so the new row (and its resolved scheme)
// reflects what was actually applied, not the form's own guess.
export const submitExposeService = (): AppThunk<Promise<void>> => async (dispatch, getState) => {
  const dialog = getState().manageDialog;
  const selection = dialog.selection;
  if (dialog.exposeBusy || !selection) {
    return;
  }
  const service = dialog.exposeForm.service.trim();
  const targetIP = dialog.exposeForm.targetIP.trim();
  if (!service || !targetIP) {
    dispatch(patchManageDialog({ exposeError: 'A service name and a target IP are required.' }));
    return;
  }
  const port = dialog.exposeForm.port.trim();
  const input: UIExposeServiceInput = {
    service,
    targetIP,
    ...(port ? { port: Number(port) } : {}),
  };
  dispatch(patchManageDialog({ exposeBusy: true, exposeError: '' }));
  try {
    await dispatch(
      environmentApi.endpoints.exposeEnvironmentService.initiate({ selection, input }),
    ).unwrap();
    if (!getState().manageDialog.open) {
      return;
    }
    dispatch(
      patchManageDialog({ exposeBusy: false, exposeForm: { service: '', targetIP: '', port: '' } }),
    );
    void dispatch(refreshManageExposures());
  } catch (error) {
    if (!getState().manageDialog.open) {
      return;
    }
    dispatch(patchManageDialog({ exposeBusy: false, exposeError: readError(error) }));
  }
};

// startUnexposeConfirm/cancelUnexposeConfirm drive the two-step destructive
// confirm below the exposure list, matching the Delete tab's own pattern
// (confirm, then a second explicit action commits it).
export const startUnexposeConfirm = (): AppThunk => (dispatch) => {
  dispatch(patchManageDialog({ unexposeConfirming: true, unexposeError: '' }));
};

export const cancelUnexposeConfirm = (): AppThunk => (dispatch) => {
  dispatch(patchManageDialog({ unexposeConfirming: false, unexposeError: '' }));
};

export const submitUnexposeEnvironment =
  (): AppThunk<Promise<void>> => async (dispatch, getState) => {
    const dialog = getState().manageDialog;
    const selection = dialog.selection;
    if (dialog.unexposeBusy || !selection) {
      return;
    }
    dispatch(patchManageDialog({ unexposeBusy: true, unexposeError: '' }));
    try {
      await dispatch(environmentApi.endpoints.unexposeEnvironment.initiate(selection)).unwrap();
      if (!getState().manageDialog.open) {
        return;
      }
      dispatch(patchManageDialog({ unexposeBusy: false, unexposeConfirming: false }));
      void dispatch(refreshManageExposures());
    } catch (error) {
      if (!getState().manageDialog.open) {
        return;
      }
      dispatch(patchManageDialog({ unexposeBusy: false, unexposeError: readError(error) }));
    }
  };
