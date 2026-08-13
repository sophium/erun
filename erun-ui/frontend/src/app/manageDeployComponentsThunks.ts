import type { UISelection } from '@/types';

import { LoadDeployComponents } from '../../wailsjs/go/main/App';
import { environmentApi } from './api/environmentApi';
import {
  deployComponentDefaultNames,
  normalizeDeployComponents,
  toggleDeployComponentName,
} from './deployComponentsSelection';
import { readError } from './errors';
import { showTerminalMessage } from './notificationThunks';
import { runtimePodConfigToKubernetes } from './runtimeResources';
import { patchManageDialog } from './slices/manageDialogSlice';
import type { AppState } from './state';
import type { AppThunk } from './store';

// Debounce the version-aware component refresh so typing a deploy version does
// not probe the registry on every keystroke (mirrors the init dialog's
// version-suggestion debounce). Module-level so rapid edits collapse to one.
let deployComponentsRefreshTimer = 0;

// clearManageDeployComponentsRefresh cancels a pending debounced refresh — call
// it when the dialog closes so a stale probe never lands on a reset dialog.
export function clearManageDeployComponentsRefresh(): void {
  if (deployComponentsRefreshTimer) {
    window.clearTimeout(deployComponentsRefreshTimer);
    deployComponentsRefreshTimer = 0;
  }
}

// scheduleManageDeployComponentsRefresh debounces a version-aware refresh so a
// version-to-deploy typed character by character triggers one registry probe.
export const scheduleManageDeployComponentsRefresh = (): AppThunk => (dispatch) => {
  clearManageDeployComponentsRefresh();
  deployComponentsRefreshTimer = window.setTimeout(() => {
    deployComponentsRefreshTimer = 0;
    void dispatch(refreshManageDeployComponents());
  }, 250);
};

// probeIsStale drops a resolved probe whose answer no longer describes what the
// dialog is showing -- the operator moved to another version, tenant, or env while
// the registry was being read, or closed the dialog altogether.
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

// The checklist is version-aware: it offers only the component charts published
// at the version this deploy would use — the picked version-to-deploy, else the
// env's current runtime version — so the probe threads that version to the
// backend. The post-resolve guard drops a stale probe when the operator has
// already moved to another version, tenant, or env.
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
          // Resolved with the checklist: which chart this version would install,
          // so a version that has none is named before Deploy, not after it fails.
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

// A draft-only edit: the selection is persisted separately via
// saveManageDeployComponents, or threaded one-shot through submitManageDeploy.
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

// Saves against the loaded config, not the working draft, so only the component
// selection is written and the operator's other unsaved edits are preserved.
// Raises the pending-redeploy banner because the selection changes what a
// redeploy rolls out.
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
      dispatch(showTerminalMessage(message));
    }
  };
