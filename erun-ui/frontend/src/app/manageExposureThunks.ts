import type { ExposeServiceFormState, UIExposeServiceInput } from '@/types';
import { defaultExposeServiceFormState } from '@/uiExposureTypes';

import { environmentApi } from './api/environmentApi';
import { readError } from './errors';
import { patchManageDialog } from './slices/manageDialogSlice';
import type { AppThunk } from './store';

// refreshManageExposures loads the Ports tab's service list. Called
// automatically when the Manage dialog opens (matching deployComponents) and
// again after a successful expose/unexpose, so the list always reflects the
// cluster's actual Services and Ingresses rather than an optimistic local
// edit.
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
    // Seed the Target IP field from the resolved local-cluster default the
    // first time the list loads, without ever overwriting something the
    // operator already typed.
    const form = getState().manageDialog.exposeForm;
    const targetIP =
      form.targetIP === '' && result.defaultTargetIP ? result.defaultTargetIP : form.targetIP;
    dispatch(
      patchManageDialog({
        exposures: result,
        exposuresLoading: false,
        exposeForm: { ...form, targetIP },
      }),
    );
  } catch (error) {
    if (!getState().manageDialog.open) {
      return;
    }
    // A round-trip failure (not a computed restricted/unconfigured result) is
    // reported the same way a genuine listing failure is, so it renders with
    // the same "failed to load" affordance rather than reading as "nothing
    // here".
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
    // The resolved hostname doesn't depend on the target IP or port, but the
    // preview call's own validation does (a target IP is required), so
    // refresh it once a value shows up rather than leaving a stale "target
    // IP required" error on screen after the operator fixes it.
    if (values.targetIP !== undefined || values.port !== undefined) {
      void dispatch(refreshExposePreview());
    }
  };

// selectExposeService picks a real Service from the picker: it records both
// the chosen Service name and the logical label erun expose would route to
// it, then resolves the preview for that pick. Passing an empty service
// clears the selection instead of resolving a preview for nothing chosen.
export const selectExposeService =
  (serviceName: string): AppThunk<Promise<void>> =>
  async (dispatch, getState) => {
    const dialog = getState().manageDialog;
    if (dialog.exposeBusy) {
      return;
    }
    const picked = dialog.exposures.services.find((service) => service.name === serviceName);
    const service = picked?.exposableLabel ?? '';
    dispatch(
      patchManageDialog({
        exposeForm: {
          ...dialog.exposeForm,
          selectedService: serviceName,
          service,
          preview: null,
          previewError: '',
        },
        exposeError: '',
      }),
    );
    if (serviceName && service) {
      await dispatch(refreshExposePreview());
    }
  };

// refreshExposePreview resolves the hostname a pick would get before the
// operator commits it (issue #1906). Silently clears the preview rather than
// erroring while required fields are still empty -- there is nothing wrong
// yet, the form just isn't ready to resolve.
export const refreshExposePreview = (): AppThunk<Promise<void>> => async (dispatch, getState) => {
  const dialog = getState().manageDialog;
  const selection = dialog.selection;
  const service = dialog.exposeForm.service.trim();
  const targetIP = dialog.exposeForm.targetIP.trim();
  if (!selection || !service || !targetIP) {
    dispatch(
      patchManageDialog({ exposeForm: { ...dialog.exposeForm, preview: null, previewError: '' } }),
    );
    return;
  }
  const port = dialog.exposeForm.port.trim();
  const input: UIExposeServiceInput = {
    service,
    targetIP,
    ...(port ? { port: Number(port) } : {}),
  };
  dispatch(
    patchManageDialog({
      exposeForm: { ...dialog.exposeForm, previewLoading: true, previewError: '' },
    }),
  );
  try {
    const preview = await dispatch(
      environmentApi.endpoints.previewExposeEnvironmentService.initiate({ selection, input }),
    ).unwrap();
    const form = getState().manageDialog;
    if (!form.open) {
      return;
    }
    dispatch(
      patchManageDialog({ exposeForm: { ...form.exposeForm, preview, previewLoading: false } }),
    );
  } catch (error) {
    const form = getState().manageDialog;
    if (!form.open) {
      return;
    }
    dispatch(
      patchManageDialog({
        exposeForm: {
          ...form.exposeForm,
          preview: null,
          previewLoading: false,
          previewError: readError(error),
        },
      }),
    );
  }
};

// submitExposeService exposes the form's picked Service at a public
// hostname, then re-reads the list from the cluster so the new row (and its
// resolved scheme) reflects what was actually applied, not the form's own
// preview.
export const submitExposeService = (): AppThunk<Promise<void>> => async (dispatch, getState) => {
  const dialog = getState().manageDialog;
  const selection = dialog.selection;
  if (dialog.exposeBusy || !selection) {
    return;
  }
  const service = dialog.exposeForm.service.trim();
  const targetIP = dialog.exposeForm.targetIP.trim();
  if (!service || !targetIP) {
    dispatch(patchManageDialog({ exposeError: 'Pick a Service and a target IP are required.' }));
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
    dispatch(patchManageDialog({ exposeBusy: false, exposeForm: defaultExposeServiceFormState() }));
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
