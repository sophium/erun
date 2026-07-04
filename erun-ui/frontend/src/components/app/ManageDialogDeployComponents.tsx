import * as React from 'react';

import {
  deployComponentLabel,
  deployComponentSelectionChanged,
} from '@/app/deployComponentsSelection';
import { environmentTypeIsRemoteWorktree } from '@/app/environmentType';
import { readError } from '@/app/errors';
import { useAppDispatch } from '@/app/hooks';
import {
  saveManageDeployComponents,
  toggleManageDeployComponent,
} from '@/app/manageEnvironmentThunks';
import { showTerminalMessage } from '@/app/notificationThunks';
import type { AppState } from '@/app/state';
import { CheckboxField } from '@/components/app/ManageDialog.fields';
import { Button } from '@/components/ui/button';

type ManageDialog = AppState['manageDialog'];

// deployComponentsCopy computes the checklist's version-dependent labels. Only a
// sourceless env's charts come from the registry by reference (filtered by the
// deploy version); a local env deploys its own working-tree charts, so the
// version neither filters them nor belongs in the heading.
function deployComponentsCopy(
  versioned: boolean,
  deployVersion: string,
): { heading: string; helper: string; loadingText: string } {
  const versionScoped = versioned && deployVersion !== '';
  return {
    heading: versionScoped ? `Components in ${deployVersion} to deploy` : 'Components to deploy',
    helper: versioned
      ? 'Deploy rolls out exactly the checked charts. Only charts published at the selected version are offered; the runtime is checked by default. Set them as the default for this environment on this machine.'
      : 'Deploy rolls out exactly the checked charts. The runtime is checked by default; set them as the default for this environment on this machine.',
    loadingText: versionScoped
      ? `Checking charts published at ${deployVersion}…`
      : 'Loading components…',
  };
}

// Toggling changes only the one-shot selection the next Deploy uses; it becomes
// this env's saved default only when the operator clicks "Set as default".
export function DeployComponentsField({ dialog }: { dialog: ManageDialog }): React.ReactElement {
  const dispatch = useAppDispatch();
  const { deployComponents, deployComponentSelection, deployComponentsLoading } = dialog;
  const selectionSet = new Set(deployComponentSelection);
  const changed = deployComponentSelectionChanged(deployComponents, deployComponentSelection);
  // Only a sourceless env's heading is version-scoped (see deployComponentsCopy).
  const versioned = environmentTypeIsRemoteWorktree(dialog.config.type);
  // The checklist follows the version chosen in the picker above — never the
  // env's current version — and is gated on a pick for every env type: the panel
  // is strictly sequential (pick a version, then choose which charts roll out),
  // exactly like Deploy. A local-agent env's create-new-version flow also reads
  // the selection, so to customize it the operator picks a version to unlock the
  // checklist (build then mints its own version and ignores the picked one).
  const deployVersion = dialog.version.trim();
  const versionPicked = deployVersion !== '';
  const gated = !versionPicked;
  const { heading, helper, loadingText } = deployComponentsCopy(versioned, deployVersion);
  return (
    // No border of its own: it nests in the version-picker popover (RuntimeTab),
    // whose wrapper draws the divider so this reads as the chosen version's charts.
    <div className="grid gap-3">
      <div className="flex items-center justify-between gap-2">
        <div
          id="environment-config-deploy-components-heading"
          className="text-xs leading-[1.2] font-semibold tracking-normal text-muted-foreground uppercase"
        >
          {heading}
        </div>
        <Button
          id="environment-config-save-deploy-components"
          type="button"
          size="sm"
          variant="outline"
          disabled={
            dialog.busy || dialog.configLoading || deployComponentsLoading || gated || !changed
          }
          onClick={() =>
            void dispatch(saveManageDeployComponents()).catch((error: unknown) => {
              dispatch(showTerminalMessage(readError(error)));
            })
          }
        >
          Set as default
        </Button>
      </div>
      {gated ? (
        <p
          id="environment-config-deploy-components-hint"
          className="text-sm leading-[1.35] text-muted-foreground"
        >
          Pick a version to deploy above, then choose which charts to roll out.
        </p>
      ) : (
        <>
          <p className="text-xs leading-[1.35] text-muted-foreground">{helper}</p>
          {deployComponentsLoading ? (
            <div className="text-sm leading-[1.35] text-muted-foreground">{loadingText}</div>
          ) : deployComponents.length === 0 ? (
            <div className="text-sm leading-[1.35] text-muted-foreground">
              No deployable components found for this environment.
            </div>
          ) : (
            <div className="grid gap-2">
              {deployComponents.map((component) => (
                <CheckboxField
                  key={component.name}
                  id={`environment-config-deploy-component-${component.name}`}
                  label={deployComponentLabel(component)}
                  checked={selectionSet.has(component.name)}
                  disabled={dialog.busy || dialog.configLoading}
                  onChange={(checked) => {
                    dispatch(toggleManageDeployComponent(component.name, checked));
                  }}
                />
              ))}
            </div>
          )}
        </>
      )}
    </div>
  );
}
