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
  // Deploys exactly what the operator checked (opt-in only); an empty set leaves
  // deploy to fall back to the env's saved default.
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

// submitCreateVersion is the explicit "create & deploy new version" action: it
// builds the env's working tree into a fresh version, pushes it, and deploys it.
// The picked version is irrelevant (build mints a new one); the checked
// components still scope what the fresh version rolls out.
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
