import { closeManageDialog } from './manageDialogThunks';
import { selectManageRuntimeImage } from './selectors';
import { startDeploySelection } from './sessionThunks';
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
  // Thread the checklist's working selection as the one-shot deploy set so what
  // rolls out is exactly what the operator sees checked (opt-in-only). Empty
  // leaves deploy to resolve the env's saved default.
  const components = [...dialog.deployComponentSelection];
  dispatch(closeManageDialog());
  await dispatch(
    startDeploySelection({
      ...selection,
      version,
      runtimeImage: version ? selectManageRuntimeImage(getState(), version) : '',
      components,
    }),
  );
};
