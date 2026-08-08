import type { UISelection } from '@/types';

import {
  ApplyPinVersion,
  ListPinnableVersions,
  PreviewPinVersion,
  RevertPinVersion,
} from '../../wailsjs/go/main/App';
import { readError } from './errors';
import type { PinPlanView } from './slices/pinVersionSlice';
import {
  closePinVersionDialog,
  openPinVersionDialog,
  setPinApplying,
  setPinError,
  setPinPlan,
  setPinPreviewing,
  setPinVersions,
  setPinVersionsError,
} from './slices/pinVersionSlice';
import type { AppThunk } from './store';

// openPinVersion opens the picker and loads the versions this environment can
// actually be pinned to, so choosing one is recognition rather than recall.
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
    const plan = (await PreviewPinVersion(selection, target)) as PinPlanView;
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
    const plan = (await ApplyPinVersion(selection, target)) as PinPlanView;
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
