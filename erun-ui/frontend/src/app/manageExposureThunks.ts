import type {
  ExposeServiceFormState,
  UIEnvironmentServiceList,
  UIExposeServiceInput,
  UIExposureList,
} from '@/types';

import { environmentApi } from './api/environmentApi';
import { readError } from './errors';
import { patchManageDialog } from './slices/manageDialogSlice';
import type { AppThunk } from './store';

export const refreshManageExposures = (): AppThunk<Promise<void>> => async (dispatch, getState) => {
  const dialog = getState().manageDialog;
  const selection = dialog.selection;
  if (!dialog.open || !selection) {
    return;
  }
  dispatch(patchManageDialog({ exposuresLoading: true }));
  // allSettled so one failed read does not blank the other panel.
  const [exposuresResult, servicesResult] = await Promise.allSettled([
    dispatch(environmentApi.endpoints.listEnvironmentExposures.initiate(selection)).unwrap(),
    dispatch(environmentApi.endpoints.listEnvironmentServices.initiate(selection)).unwrap(),
  ]);
  if (!getState().manageDialog.open) {
    return;
  }
  // A round-trip failure renders as "failed to load", not "nothing exposed".
  const exposures: UIExposureList =
    exposuresResult.status === 'fulfilled'
      ? exposuresResult.value
      : {
          configured: true,
          restricted: false,
          error: readError(exposuresResult.reason),
          services: [],
        };
  const environmentServices: UIEnvironmentServiceList =
    servicesResult.status === 'fulfilled'
      ? servicesResult.value
      : {
          configured: true,
          restricted: false,
          error: readError(servicesResult.reason),
          services: [],
        };
  dispatch(
    patchManageDialog({
      exposures,
      environmentServices,
      exposuresLoading: false,
    }),
  );
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
  const backendService = dialog.exposeForm.backendService.trim();
  const input: UIExposeServiceInput = {
    service,
    targetIP,
    // Omitted, the backend derives <tenant>-<service>.
    ...(backendService ? { backendService } : {}),
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
      patchManageDialog({
        exposeBusy: false,
        exposeForm: { service: '', backendService: '', targetIP: '', port: '' },
      }),
    );
    void dispatch(refreshManageExposures());
  } catch (error) {
    if (!getState().manageDialog.open) {
      return;
    }
    dispatch(patchManageDialog({ exposeBusy: false, exposeError: readError(error) }));
  }
};

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
