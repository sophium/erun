import { Button } from 'erun-kit';
import { AlertTriangle } from 'lucide-react';
import * as React from 'react';

import { useAppDispatch } from '@/app/hooks';
import { updateManageConfig } from '@/app/manageEnvironmentThunks';
import { runtimeChartChoices } from '@/app/runtimeChartChoices';
import {
  RUNTIME_CHART_NOTICE_ID,
  runtimeChartBlocksDeploy,
  runtimeChartUnsaved,
  savedRuntimeChartLabel,
  statedRuntimeChart,
} from '@/app/runtimeChartPlan';
import type { AppState } from '@/app/state';

type ManageDialog = AppState['manageDialog'];

// RuntimeChartNotice states what a deploy of the picked version would install for
// the runtime, immediately under the version row and immediately above the field
// that changes it -- the blocking reason and its fix, adjacent, where the operator
// is acting rather than inside a popover that closes.
export function RuntimeChartNotice({
  dialog,
  id = RUNTIME_CHART_NOTICE_ID,
}: {
  dialog: ManageDialog;
  // The picker panel renders its own instance while it is open, so each instance
  // derives every id it owns from this one -- including the recovery button, which
  // would otherwise collide with its twin during the panel's close animation.
  id?: string;
}): React.ReactElement | null {
  const dispatch = useAppDispatch();
  const plan = dialog.runtimeChartPlan;
  const offer = runtimeChartChoices(dialog.versionSuggestions)[0];

  if (runtimeChartUnsaved(dialog)) {
    return (
      <Notice id={id}>
        The runtime chart below is unsaved. Deploy installs the saved one
        {savedRuntimeChartLabel(dialog)} &mdash; save first.
      </Notice>
    );
  }
  if (runtimeChartBlocksDeploy(dialog)) {
    return (
      <Notice id={id}>
        <span>
          No runtime chart is published at {plan?.version}. That version names the image; the chart
          is ERun&apos;s and ships on ERun&apos;s versions. Choose one below to deploy it.
        </span>
        {offer && (
          <Button
            id={`${id}-adopt`}
            type="button"
            size="sm"
            variant="outline"
            className="mt-1 justify-self-start"
            disabled={dialog.busy || dialog.configLoading}
            onClick={() => {
              dispatch(updateManageConfig({ runtimeChart: offer.reference }));
            }}
          >
            Use {offer.label}
          </Button>
        )}
      </Notice>
    );
  }
  const stated = statedRuntimeChart(dialog, plan?.version ?? '');
  if (stated) {
    // Visibility of system status: with the two coordinates apart, "what will be
    // installed" is two facts, and this is the one the version cannot show. Read
    // from the environment's saved chart rather than the probe, which was resolved
    // before the chart was stated and would still be describing the old answer.
    return (
      <div id={id} className="text-[12px] leading-[1.4] text-muted-foreground">
        Runtime chart {stated.chart} {stated.version}, set on this environment.
      </div>
    );
  }
  return null;
}

function Notice({ id, children }: { id: string; children: React.ReactNode }): React.ReactElement {
  return (
    <div
      id={id}
      role="status"
      className="grid gap-1 rounded-[var(--radius)] border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-[12px] leading-[1.4] text-foreground"
    >
      <span className="flex items-start gap-2">
        <AlertTriangle aria-hidden="true" className="mt-[1px] size-3.5 shrink-0" />
        <span className="grid gap-1">{children}</span>
      </span>
    </div>
  );
}
