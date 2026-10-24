import { closeManageDialog } from './manageDialogThunks';
import { selectManageRuntimeImage } from './selectors';
import { startCreateVersionSelection, startDeploySelection } from './sessionThunks';
import type { AppThunk } from './store';
import { normalizeDialogValue } from './versionSuggestions';

export const submitManageDeploy = (): AppThunk<Promise<void>> => async (dispatch, getState) => {
  const dialog = getState().manageDialog;
  if (dialog.busy) {
    return;
  }
  const selection = dialog.selection;
  if (!selection) {
    dispatch(closeManageDialog());
    return;
  }
  const version = normalizeDialogValue(dialog.version);
  // Empty only from callers that deploy the runtime on purpose.
  const components = [...dialog.deployComponentSelection];
  // Resolve before closeManageDialog resets the suggestions it reads; otherwise
  // --runtime-image is dropped and the wrong version deploys.
  const runtimeImage = version ? selectManageRuntimeImage(getState(), version) : '';
  dispatch(closeManageDialog());
  await dispatch(
    startDeploySelection({
      ...selection,
      version,
      runtimeImage,
      components,
    }),
  );
};

// submitCreateVersion builds and deploys a fresh version; the picked version is
// ignored.
export const submitCreateVersion = (): AppThunk<Promise<void>> => async (dispatch, getState) => {
  const dialog = getState().manageDialog;
  if (dialog.busy) {
    return;
  }
  const selection = dialog.selection;
  if (!selection) {
    dispatch(closeManageDialog());
    return;
  }
  const components = [...dialog.deployComponentSelection];
  dispatch(closeManageDialog());
  await dispatch(startCreateVersionSelection({ ...selection, components }));
};
