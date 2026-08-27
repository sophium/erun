import type { UISelection } from '@/types';

import {
  ApplyPinVersion,
  ListPinnableVersions,
  PinRepoCheckoutStatus,
  PreviewPinVersion,
  RevertPinVersion,
} from '../../wailsjs/go/main/App';
import { readError } from './errors';
import type { PinPlanView } from './slices/pinVersionSlice';
import {
  closePinVersionDialog,
  openPinVersionDialog,
  PIN_LATEST_STABLE_TARGET,
  setPinApplying,
  setPinError,
  setPinPlan,
  setPinPreviewing,
  setPinRepoCheckoutStatus,
  setPinVersions,
  setPinVersionsError,
} from './slices/pinVersionSlice';
import type { AppThunk } from './store';

// resolvePinTargetForCall maps the dialog's "no explicit choice" sentinel back
// to '', which is what erun pin (via the Go bindings) has always read as
// "resolve the latest published stable release" — the sentinel exists only so
// the Version select has a real, selectable value for that choice.
function resolvePinTargetForCall(target: string): string {
  return target === PIN_LATEST_STABLE_TARGET ? '' : target;
}

// openPinVersion opens the picker and loads the versions this environment can
// actually be pinned to, so choosing one is recognition rather than recall. It
// also checks up front whether this environment's references even have a
// resolvable local checkout — a sourceless runtime environment has none of
// its own, and the dialog needs to say so before offering Preview/Apply that
// can never resolve, not just report it after a failed click.
export const openPinVersion =
  (selection: UISelection): AppThunk<Promise<void>> =>
  async (dispatch) => {
    dispatch(openPinVersionDialog(selection));
    try {
      const versions = await ListPinnableVersions(selection);
      dispatch(setPinVersions(versions));
    } catch (error) {
      dispatch(setPinVersionsError(readError(error)));
    }
    try {
      const status = await PinRepoCheckoutStatus(selection);
      dispatch(
        setPinRepoCheckoutStatus({ resolvable: status.resolvable, reason: status.reason ?? '' }),
      );
    } catch (error) {
      dispatch(setPinRepoCheckoutStatus({ resolvable: false, reason: readError(error) }));
    }
  };

// previewPin resolves the full plan without writing. The dialog always shows it
// before applying: a re-pin edits files across a repo.
export const previewPin = (): AppThunk<Promise<void>> => async (dispatch, getState) => {
  const { selection, target } = getState().pinVersion;
  if (!selection) {
    return;
  }
  dispatch(setPinPreviewing(true));
  try {
    const plan = (await PreviewPinVersion(
      selection,
      resolvePinTargetForCall(target),
    )) as PinPlanView;
    dispatch(setPinPlan({ plan, applied: false }));
  } catch (error) {
    dispatch(setPinError(readError(error)));
  }
};

export const applyPin = (): AppThunk<Promise<void>> => async (dispatch, getState) => {
  const { selection, target } = getState().pinVersion;
  if (!selection) {
    return;
  }
  dispatch(setPinApplying(true));
  try {
    const plan = (await ApplyPinVersion(selection, resolvePinTargetForCall(target))) as PinPlanView;
    dispatch(setPinPlan({ plan, applied: true }));
  } catch (error) {
    dispatch(setPinError(readError(error)));
  }
};

// revertPin goes back to the version recorded before the last re-pin, which is
// what makes trying one out cheap.
export const revertPin = (): AppThunk<Promise<void>> => async (dispatch, getState) => {
  const { selection } = getState().pinVersion;
  if (!selection) {
    return;
  }
  dispatch(setPinApplying(true));
  try {
    const plan = (await RevertPinVersion(selection)) as PinPlanView;
    dispatch(setPinPlan({ plan, applied: true }));
  } catch (error) {
    dispatch(setPinError(readError(error)));
  }
};

export const closePinVersion = (): AppThunk => (dispatch) => {
  dispatch(closePinVersionDialog());
};
