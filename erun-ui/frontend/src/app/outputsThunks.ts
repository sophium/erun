import type { AgentOutputsList } from '@/types';

import {
  DownloadAgentOutput,
  DownloadOrchestratorOutput,
  ListAgentOutputs,
  ListOrchestratorOutputs,
  RunHostArtifact,
  RunOrchestratorOutputOnHost,
} from '../../wailsjs/go/main/App';
import { readError } from './errors';
import type { OutputsTarget } from './slices/outputsDialogSlice';
import {
  openOutputsDialog,
  setOutputs,
  setOutputsDownloading,
  setOutputsError,
  setOutputsRunning,
  setOutputsStatus,
} from './slices/outputsDialogSlice';
import type { AppThunk } from './store';

export const openOutputs =
  (target: OutputsTarget): AppThunk<Promise<void>> =>
  async (dispatch) => {
    dispatch(openOutputsDialog(target));
    try {
      const list = (await (target.kind === 'orchestrator'
        ? ListOrchestratorOutputs(target.orchestratorId)
        : ListAgentOutputs(target.selection))) as AgentOutputsList;
      dispatch(setOutputs({ dir: list.dir, entries: list.entries }));
    } catch (error) {
      dispatch(setOutputsError(readError(error)));
    }
  };

// An empty path back means the operator cancelled.
export const downloadOutput =
  (name: string): AppThunk<Promise<void>> =>
  async (dispatch, getState) => {
    const target = getState().outputsDialog.target;
    if (!target) {
      return;
    }
    dispatch(setOutputsDownloading(name));
    try {
      const dest = await (target.kind === 'orchestrator'
        ? DownloadOrchestratorOutput(target.orchestratorId, name)
        : DownloadAgentOutput(target.selection, name));
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

// runOutputOnHost runs an environment's artifact from its synced host mirror, and
// an orchestrator's in place.
export const runOutputOnHost =
  (name: string): AppThunk<Promise<void>> =>
  async (dispatch, getState) => {
    const target = getState().outputsDialog.target;
    if (!target) {
      return;
    }
    dispatch(setOutputsRunning(name));
    try {
      await (target.kind === 'orchestrator'
        ? RunOrchestratorOutputOnHost(target.orchestratorId, name)
        : RunHostArtifact(target.selection, name));
      dispatch(setOutputsStatus({ message: `Launched ${name} on this machine.`, error: false }));
    } catch (error) {
      dispatch(setOutputsStatus({ message: readError(error), error: true }));
    }
  };
