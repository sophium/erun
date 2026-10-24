import type { UISelection } from '@/types';

import { LoadDeployComponents } from '../../wailsjs/go/main/App';
import { environmentApi } from './api/environmentApi';
import {
  deployComponentDefaultNames,
  normalizeDeployComponents,
  toggleDeployComponentName,
} from './deployComponentsSelection';
import { readError } from './errors';
import { showTerminalError } from './notificationThunks';
import { runtimePodConfigToKubernetes } from './runtimeResources';
import { patchManageDialog } from './slices/manageDialogSlice';
import type { AppState } from './state';
import type { AppThunk } from './store';

let deployComponentsRefreshTimer = 0;

// Call clearManageDeployComponentsRefresh on close so a stale probe never lands.
export function clearManageDeployComponentsRefresh(): void {
  if (deployComponentsRefreshTimer) {
    window.clearTimeout(deployComponentsRefreshTimer);
    deployComponentsRefreshTimer = 0;
  }
}

export const scheduleManageDeployComponentsRefresh = (): AppThunk => (dispatch) => {
  clearManageDeployComponentsRefresh();
  deployComponentsRefreshTimer = window.setTimeout(() => {
    deployComponentsRefreshTimer = 0;
    void dispatch(refreshManageDeployComponents());
  }, 250);
};

function probeIsStale(
  dialog: AppState['manageDialog'],
  selection: UISelection,
  version: string,
): boolean {
  const current = dialog.selection;
  if (!dialog.open || !current) {
    return true;
  }
  return (
    current.tenant !== selection.tenant ||
    current.environment !== selection.environment ||
    dialog.version.trim() !== version
  );
}

// refreshManageDeployComponents offers only the charts published at the version
// this deploy would use.
export const refreshManageDeployComponents =
  (): AppThunk<Promise<void>> => async (dispatch, getState) => {
    const dialog = getState().manageDialog;
    const selection = dialog.selection;
    if (!selection) {
      return;
    }
    const version = dialog.version.trim();
    dispatch(patchManageDialog({ deployComponentsLoading: true }));
    try {
      const raw = await LoadDeployComponents({ ...selection, version });
      if (probeIsStale(getState().manageDialog, selection, version)) {
        return;
      }
      const options = normalizeDeployComponents(raw.components);
      dispatch(
        patchManageDialog({
          deployComponents: options,
          deployComponentSelection: deployComponentDefaultNames(options),
          deployComponentsLoading: false,
          runtimeChartPlan: raw.runtimeChart,
        }),
      );
    } catch (error) {
      if (!getState().manageDialog.open) {
        return;
      }
      dispatch(patchManageDialog({ deployComponentsLoading: false, error: readError(error) }));
    }
  };

export const toggleManageDeployComponent =
  (name: string, checked: boolean): AppThunk =>
  (dispatch, getState) => {
    const dialog = getState().manageDialog;
    if (dialog.busy) {
      return;
    }
    dispatch(
      patchManageDialog({
        deployComponentSelection: toggleDeployComponentName(
          dialog.deployComponents,
          dialog.deployComponentSelection,
          name,
          checked,
        ),
      }),
    );
  };

// Saves against the loaded config so other unsaved edits are preserved.
export const saveManageDeployComponents =
  (): AppThunk<Promise<void>> => async (dispatch, getState) => {
    const dialog = getState().manageDialog;
    const selection = dialog.selection;
    const base = dialog.initialConfig;
    if (dialog.busy || dialog.configLoading || !selection || !base) {
      return;
    }
    const componentSelection = [...dialog.deployComponentSelection];
    dispatch(
      patchManageDialog({
        busy: true,
        busyAction: 'save',
        busyTarget: 'deploy-components',
        error: '',
      }),
    );
    try {
      const saveConfig = {
        ...base,
        runtimePod: runtimePodConfigToKubernetes(base.runtimePod),
        deployComponents: componentSelection,
      };
      const result = await dispatch(
        environmentApi.endpoints.saveEnvironmentConfig.initiate({ selection, config: saveConfig }),
      ).unwrap();
      if (!getState().manageDialog.open) {
        return;
      }
      const savedSelection = result.deployComponents ?? [];
      const latest = getState().manageDialog;
      dispatch(
        patchManageDialog({
          config: { ...latest.config, deployComponents: savedSelection },
          initialConfig: latest.initialConfig
            ? { ...latest.initialConfig, deployComponents: savedSelection }
            : latest.initialConfig,
          deployComponents: latest.deployComponents.map((option) => ({
            ...option,
            selected: savedSelection.includes(option.name),
          })),
          deployComponentSelection: savedSelection,
          busy: false,
          busyAction: '',
          busyTarget: '',
          error: '',
          pendingRedeploy: true,
        }),
      );
    } catch (error) {
      const message = readError(error);
      dispatch(patchManageDialog({ busy: false, busyAction: '', busyTarget: '', error: message }));
      dispatch(showTerminalError(message));
    }
  };
