import { Button } from 'erun-kit';
import { Rocket } from 'lucide-react';
import * as React from 'react';

import {
  DEPLOY_SELECTION_NOTICE_ID,
  deploySelectionIsEmpty,
} from '@/app/deployComponentsSelection';
import { RUNTIME_CHART_NOTICE_ID, runtimeChartBlocksDeploy } from '@/app/runtimeChartPlan';
import type { AppState } from '@/app/state';

type ManageDialog = AppState['manageDialog'];

// RuntimeDeployButton is the Runtime tab's Deploy control, extracted from
// RuntimeDeployField to keep that function inside its size and complexity budget.
//
// Deploy installs a chosen version by reference, so it stays disabled until the
// operator picks one — never a build, never a guess — and until that version's
// component charts have been probed, so it cannot fire the new version with the
// previous version's chart selection. It refuses two further states that would
// otherwise roll out something the operator did not choose: a version the registry
// says has no runtime chart, and a checklist with every box cleared — an empty set
// reaches the resolver as "unspecified" and falls back to the runtime chart alone
// (see deploySelectionIsEmpty). Each reason is named beside the button and
// described by it, rather than discovered by a failed rollout.
export function RuntimeDeployButton({
  dialog,
  disabled,
  onDeploy,
}: {
  dialog: ManageDialog;
  disabled?: boolean;
  onDeploy: () => void;
}): React.ReactElement {
  const versionPicked = dialog.version.trim() !== '';
  const chartBlocked = runtimeChartBlocksDeploy(dialog);
  const selectionEmpty = deploySelectionIsEmpty(
    dialog.deployComponents,
    dialog.deployComponentSelection,
    dialog.deployComponentsLoading,
  );
  return (
    <Button
      id="environment-config-deploy"
      type="button"
      size="sm"
      disabled={
        disabled === true ||
        !versionPicked ||
        dialog.deployComponentsLoading ||
        chartBlocked ||
        selectionEmpty
      }
      aria-describedby={
        chartBlocked
          ? RUNTIME_CHART_NOTICE_ID
          : selectionEmpty
            ? DEPLOY_SELECTION_NOTICE_ID
            : undefined
      }
      onClick={onDeploy}
    >
      <Rocket aria-hidden="true" />
      Deploy
    </Button>
  );
}
