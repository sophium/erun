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

// '' is what erun pin reads as "latest stable"; the sentinel exists only so the
// select has a value for it.
function resolvePinTargetForCall(target: string): string {
  return target === PIN_LATEST_STABLE_TARGET ? '' : target;
}

// openPinVersion also checks for a resolvable local checkout up front, so a
// sourceless environment is told before Preview/Apply.
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

// previewPin always runs before apply: a re-pin edits files across a repo.
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
