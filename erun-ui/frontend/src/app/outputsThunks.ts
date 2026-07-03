import type { AgentOutputsList, UISelection } from '@/types';

import { DownloadAgentOutput, ListAgentOutputs } from '../../wailsjs/go/main/App';
import { readError } from './errors';
import {
  openOutputsDialog,
  setOutputs,
  setOutputsDownloading,
  setOutputsError,
  setOutputsStatus,
} from './slices/outputsDialogSlice';
import type { AppThunk } from './store';

// openOutputs surfaces the files an agent produced in its runtime pod, newest-first.
export const openOutputs =
  (selection: UISelection): AppThunk<Promise<void>> =>
  async (dispatch) => {
    dispatch(openOutputsDialog(selection));
    try {
      const list = (await ListAgentOutputs(selection)) as AgentOutputsList;
      dispatch(setOutputs({ dir: list.dir, entries: list.entries }));
    } catch (error) {
      dispatch(setOutputsError(readError(error)));
    }
  };

// downloadOutput saves one entry through a native Save dialog; an empty path back means the operator cancelled.
export const downloadOutput =
  (name: string): AppThunk<Promise<void>> =>
  async (dispatch, getState) => {
    const selection = getState().outputsDialog.selection;
    if (!selection) {
      return;
    }
    dispatch(setOutputsDownloading(name));
    try {
      const dest = await DownloadAgentOutput(selection, name);
      dispatch(
        setOutputsStatus(
          dest.trim() === ''
            ? { message: `Download of ${name} cancelled.`, error: false }
            : { message: `Saved ${name} to ${dest}`, error: false },
        ),
      );
    } catch (error) {
      dispatch(setOutputsStatus({ message: readError(error), error: true }));
    }
  };
