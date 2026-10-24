import {
  Button,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  EmptyState,
} from 'erun-kit';
import * as React from 'react';

import {
  buildProfileViewButtonLabel,
  cgroupIsUsable,
  cpuLabel,
  formatBytes,
  formatDurationSeconds,
  ioLabel,
  throttleRatioLabel,
} from '@/app/buildProfileFormat';
import type { UIBuildProfileStep, UIBuildProfileSummary, UITenantDashboardBuild } from '@/types';

import { DataCell, DataTable } from './TenantDashboardMessage';

// BuildProfileDialog renders already-loaded data, so it needs no fetch.
export function BuildProfileDialog({
  build,
  onClose,
}: {
  build: UITenantDashboardBuild | null;
  onClose: () => void;
}): React.ReactElement {
  return (
    <Dialog
      open={build !== null}
      onOpenChange={(next) => {
        if (!next) {
          onClose();
        }
      }}
    >
      <DialogContent className="flex max-h-[85vh] flex-col overflow-hidden sm:max-w-2xl">
        {build && <BuildProfileBody build={build} />}
      </DialogContent>
    </Dialog>
  );
}

function BuildProfileBody({ build }: { build: UITenantDashboardBuild }): React.ReactElement {
  return (
    <>
      <DialogHeader>
        <DialogTitle>Build profile</DialogTitle>
        <DialogDescription className="font-mono text-xs">{build.commitId}</DialogDescription>
      </DialogHeader>
      {build.profile ? <BuildProfileSummaryView profile={build.profile} /> : <NoBuildProfile />}
    </>
  );
}

function NoBuildProfile(): React.ReactElement {
  return (
    <div className="mt-4">
      <EmptyState
        heading="No profile collected for this build"
        body="This build ran before profiling was available, or its own erun version collected none."
      />
    </div>
  );
}

function BuildProfileSummaryView({
  profile,
}: {
  profile: UIBuildProfileSummary;
}): React.ReactElement {
  const steps = profile.topSteps ?? [];
  return (
    <div className="mt-4 flex min-h-0 flex-col gap-3 overflow-auto">
      <div className="text-sm text-muted-foreground">
        Total duration {formatDurationSeconds(profile.durationSeconds)}
        {profile.failed ? ' — failed' : ''}
      </div>
      {!buildProfileHasCgroupData(profile) && (
        <div role="status" className="text-sm text-muted-foreground">
          CPU and I/O metrics are not available for this build.
        </div>
      )}
      {steps.length > 0 ? (
        <BuildProfileStepsTable steps={steps} />
      ) : (
        <EmptyState heading="No steps recorded" />
      )}
      {profile.truncatedStepCount !== undefined && profile.truncatedStepCount > 0 && (
        <div className="text-xs text-muted-foreground">
          {profile.truncatedStepCount} more step{profile.truncatedStepCount === 1 ? '' : 's'} not
          shown (showing the {steps.length} costliest).
        </div>
      )}
    </div>
  );
}

// True if any step carries cgroup data, so a mixed build is not told it has none.
function buildProfileHasCgroupData(profile: UIBuildProfileSummary): boolean {
  if (cgroupIsUsable(profile.cgroup)) {
    return true;
  }
  return (profile.topSteps ?? []).some((step) => cgroupIsUsable(step.cgroup));
}

function BuildProfileStepsTable({ steps }: { steps: UIBuildProfileStep[] }): React.ReactElement {
  return (
    <DataTable
      headers={['Step', 'Duration', 'CPU', 'Throttling', 'I/O', 'Peak memory']}
      minWidthClassName="min-w-[640px]"
    >
      {steps.map((step, index) => (
        <tr key={`${step.name}-${String(index)}`}>
          <DataCell strong>{step.name}</DataCell>
          <DataCell>{formatDurationSeconds(step.durationSeconds)}</DataCell>
          <DataCell>
            {cgroupIsUsable(step.cgroup) ? cpuLabel(step.cgroup) : 'Not available'}
          </DataCell>
          <DataCell>
            {cgroupIsUsable(step.cgroup)
              ? (throttleRatioLabel(step.cgroup) ?? '—')
              : 'Not available'}
          </DataCell>
          <DataCell>
            {cgroupIsUsable(step.cgroup) ? (ioLabel(step.cgroup) ?? '—') : 'Not available'}
          </DataCell>
          <DataCell>
            {cgroupIsUsable(step.cgroup)
              ? formatBytes(step.cgroup.peakMemoryBytes)
              : 'Not available'}
          </DataCell>
        </tr>
      ))}
    </DataTable>
  );
}

export function BuildProfileViewButton({
  build,
  onSelect,
}: {
  build: UITenantDashboardBuild;
  onSelect: (build: UITenantDashboardBuild) => void;
}): React.ReactElement {
  return (
    <Button
      type="button"
      variant="ghost"
      size="sm"
      aria-label={buildProfileViewButtonLabel(build.buildId)}
      onClick={() => {
        onSelect(build);
      }}
    >
      View profile
    </Button>
  );
}
