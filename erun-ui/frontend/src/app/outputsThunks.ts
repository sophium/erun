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

// openOutputs surfaces the files an agent produced, newest-first — from the
// environment's runtime pod, or from this host for an orchestrator.
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

// downloadOutput saves one entry through a native Save dialog; an empty path back means the operator cancelled.
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

// runOutputOnHost launches an artifact on this machine. For an environment that
// is the copy workspace sync mirrored down from the pod, so it errors clearly
// when the env has no host workspace or the artifact has not synced yet. For an
// orchestrator the file was produced here, so it runs in place.
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
