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

// openOutputs opens the Outputs dialog for an env and lists the files an agent
// produced in its runtime pod outputs directory, newest-first. Read-only.
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

// downloadOutput downloads one entry from the pod through a native Save dialog
// (the backend writes the chosen path). It reports the outcome — saved path,
// cancelled, or error — so the operator can tell whether their click succeeded
// (Nielsen #1 visibility of system status, #9 recovery from errors).
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
