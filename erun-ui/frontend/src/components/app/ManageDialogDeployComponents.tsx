import { Button } from 'erun-kit';
import { AlertTriangle } from 'lucide-react';
import * as React from 'react';

import {
  DEPLOY_COMPONENTS_EMPTY_NOTICE_ID,
  DEPLOY_COMPONENTS_EMPTY_PANEL_NOTICE_ID,
  deployComponentLabel,
  deployComponentSelectionChanged,
  deployComponentsEmptySelection,
} from '@/app/deployComponentsSelection';
import { readError } from '@/app/errors';
import { useAppDispatch } from '@/app/hooks';
import {
  saveManageDeployComponents,
  toggleManageDeployComponent,
} from '@/app/manageEnvironmentThunks';
import { showTerminalError } from '@/app/notificationThunks';
import type { AppState } from '@/app/state';
import { CheckboxField } from '@/components/app/ManageDialog.fields';

type ManageDialog = AppState['manageDialog'];

function deployComponentsCopy(deployVersion: string): {
  heading: string;
  helper: string;
  loadingText: string;
} {
  return {
    heading: `Components in ${deployVersion} to deploy`,
    helper:
      'Deploy rolls out exactly the checked charts for the selected version. The runtime is checked by default; set them as the default for this environment on this machine.',
    loadingText: `Checking components for ${deployVersion}…`,
  };
}

// Toggling changes only the next Deploy; "Set as default" saves it.
export function DeployComponentsField({ dialog }: { dialog: ManageDialog }): React.ReactElement {
  const dispatch = useAppDispatch();
  const { deployComponents, deployComponentSelection, deployComponentsLoading } = dialog;
  const selectionSet = new Set(deployComponentSelection);
  const changed = deployComponentSelectionChanged(deployComponents, deployComponentSelection);
  // Follows the picked version, never the env's current one, and is gated on a pick for every env type.
  const deployVersion = dialog.version.trim();
  const versionPicked = deployVersion !== '';
  const gated = !versionPicked;
  const { heading, helper, loadingText } = deployComponentsCopy(deployVersion);
  return (
    // No border: the version-picker popover's wrapper draws the divider.
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
              dispatch(showTerminalError(readError(error)));
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
          {/* Panel id avoids colliding with the row's twin during the close animation. */}
          <DeployComponentsEmptyNotice
            dialog={dialog}
            id={DEPLOY_COMPONENTS_EMPTY_PANEL_NOTICE_ID}
          />
        </>
      )}
    </div>
  );
}

// DeployComponentsEmptyNotice explains why Deploy is disabled on an empty checklist.
export function DeployComponentsEmptyNotice({
  dialog,
  id = DEPLOY_COMPONENTS_EMPTY_NOTICE_ID,
}: {
  dialog: ManageDialog;
  id?: string;
}): React.ReactElement | null {
  if (!deployComponentsEmptySelection(dialog)) {
    return null;
  }
  return (
    <div
      id={id}
      role="status"
      className="mt-2 grid gap-1 rounded-[var(--radius)] border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-[12px] leading-[1.4] text-foreground"
    >
      <span className="flex items-start gap-2">
        <AlertTriangle aria-hidden="true" className="mt-[1px] size-3.5 shrink-0" />
        <span>
          No charts are checked, so nothing would roll out. Deploy installs exactly the checked
          charts &mdash; check at least one above to deploy.
        </span>
      </span>
    </div>
  );
}
