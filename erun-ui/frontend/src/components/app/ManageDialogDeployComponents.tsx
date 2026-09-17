import { Button } from 'erun-kit';
import { AlertTriangle } from 'lucide-react';
import * as React from 'react';

import {
  DEPLOY_SELECTION_PANEL_NOTICE_ID,
  deployComponentLabel,
  deployComponentSelectionChanged,
  deploySelectionIsEmpty,
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

// deployComponentsCopy computes the checklist's labels. The experience is the
// same for every env type: the checklist only shows once a version is picked
// (see the gate below), so it is always scoped to that version — the wording
// never branches on whether the env deploys published or working-tree charts.
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

// Toggling changes only the one-shot selection the next Deploy uses; it becomes
// this env's saved default only when the operator clicks "Set as default".
export function DeployComponentsField({ dialog }: { dialog: ManageDialog }): React.ReactElement {
  const dispatch = useAppDispatch();
  const { deployComponents, deployComponentSelection, deployComponentsLoading } = dialog;
  const selectionSet = new Set(deployComponentSelection);
  const changed = deployComponentSelectionChanged(deployComponents, deployComponentSelection);
  // Nothing checked here is an explicit statement, not an absent one — see
  // deploySelectionIsEmpty. It refuses Deploy, and this names the reason where the
  // operator is looking rather than leaving a dead button.
  const selectionEmpty = deploySelectionIsEmpty(
    deployComponents,
    deployComponentSelection,
    deployComponentsLoading,
  );
  // The checklist follows the version chosen in the picker above — never the
  // env's current version — and is gated on a pick for every env type: the panel
  // is strictly sequential (pick a version, then choose which charts roll out),
  // exactly like Deploy. A local-agent env's create-new-version flow also reads
  // the selection, so to customize it the operator picks a version to unlock the
  // checklist (build then mints its own version and ignores the picked one).
  const deployVersion = dialog.version.trim();
  const versionPicked = deployVersion !== '';
  const gated = !versionPicked;
  const { heading, helper, loadingText } = deployComponentsCopy(deployVersion);
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
            <>
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
              {selectionEmpty && <EmptySelectionNotice id={DEPLOY_SELECTION_PANEL_NOTICE_ID} />}
            </>
          )}
        </>
      )}
    </div>
  );
}

// EmptySelectionNotice names why Deploy is refused when every box is unchecked.
// The checklist's sentence above covers the checked case alone, and the resolver
// reads an empty set as "unspecified" and falls back to the runtime chart alone
// (erun-common/deploy_components.go), so submitting it would roll out the very
// chart the operator cleared. Rendered beside the boxes and beside the button; the
// fix is one click away in the list, so there is nothing else to offer here.
export function EmptySelectionNotice({ id }: { id: string }): React.ReactElement {
  return (
    <div
      id={id}
      role="status"
      className="flex items-start gap-2 rounded-[var(--radius)] border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-[12px] leading-[1.4] text-foreground"
    >
      <AlertTriangle aria-hidden="true" className="mt-[1px] size-3.5 shrink-0" />
      <span>
        Nothing is checked, so Deploy has nothing to roll out. Check at least one chart &mdash; an
        empty list would otherwise still install the runtime chart.
      </span>
    </div>
  );
}
