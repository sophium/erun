import { Button } from 'erun-kit';
import { RefreshCw } from 'lucide-react';
import * as React from 'react';

import { useGetRuntimeUsageQuery } from '@/app/api/environmentApi';
import type { UISelection } from '@/types';
import type {
  UIRuntimeCPUUsage,
  UIRuntimeDiskUsage,
  UIRuntimeMemoryUsage,
  UIRuntimeUsage,
} from '@/uiRuntimeTypes';

// RuntimeUsageField answers "how close is this environment to its own
// limits" right beside the sliders that move those limits. Unlike
// RuntimeResourceControls (a reading of the node), this is the environment's
// own opinion: CPU against its quota, memory current/peak against its own
// cgroup limit with the real OOM-kill count, and disk on the workspace mount.
//
// Fail-soft by contract (Nielsen #1, visibility of system status): a field
// the reader could not measure (cgroup v1, an unlimited limit, an unreadable
// file) renders its own "unavailable" state, never a confident 0 or 0%, since
// that would read as "idle" or "empty" rather than "unknown".
export function RuntimeUsageField({
  selection,
  disabled,
}: {
  selection: UISelection;
  disabled: boolean;
}): React.ReactElement {
  const { data, isFetching, refetch } = useGetRuntimeUsageQuery(selection);
  const busy = disabled || isFetching;

  return (
    <div className="grid gap-3 rounded-[var(--radius)] border border-border p-3">
      <div className="flex items-center justify-between gap-3">
        <div className="text-xs leading-[1.2] font-semibold tracking-normal text-muted-foreground uppercase">
          This environment&apos;s usage
        </div>
        <Button
          id="environment-config-usage-refresh"
          type="button"
          size="sm"
          variant="ghost"
          className="h-7 px-2"
          aria-label="Refresh this environment's usage"
          disabled={busy}
          onClick={() => void refetch()}
        >
          <RefreshCw aria-hidden="true" />
        </Button>
      </div>
      <RuntimeUsageSummary
        loading={isFetching && !data}
        available={data?.available === true}
        message={data?.message ?? ''}
      />
      <RuntimeUsageDetails data={data} />
    </div>
  );
}

// Split out so the available/unavailable branching for the figures and the
// warnings both live in one place, keeping RuntimeUsageField's own branching
// within budget.
function RuntimeUsageDetails({
  data,
}: {
  data: UIRuntimeUsage | undefined;
}): React.ReactElement | null {
  if (!data?.available) {
    return null;
  }
  return (
    <>
      <dl className="grid grid-cols-[max-content_1fr] gap-x-2 gap-y-1 text-xs leading-[1.35]">
        <CPURow cpu={data.cpu} />
        <MemoryRows memory={data.memory} />
        {(data.disk ?? []).map((disk) => (
          <DiskRow key={disk.mount} disk={disk} />
        ))}
      </dl>
      <RuntimeUsageWarnings warnings={data.warnings} />
    </>
  );
}

function RuntimeUsageSummary({
  loading,
  available,
  message,
}: {
  loading: boolean;
  available: boolean;
  message: string;
}): React.ReactElement {
  if (loading) {
    return <p className="text-xs leading-[1.35] text-muted-foreground">Reading the runtime...</p>;
  }
  // An unreachable pod explains itself rather than rendering an empty panel
  // that reads as "nothing to see".
  return (
    <p
      className={
        available
          ? 'text-xs leading-[1.35] text-muted-foreground'
          : 'text-xs leading-[1.35] text-amber-600 dark:text-amber-400'
      }
      role="status"
    >
      {message || 'Open the environment to see its own resource usage.'}
    </p>
  );
}

function RuntimeUsageWarnings({
  warnings,
}: {
  warnings: string[] | undefined;
}): React.ReactElement | null {
  if (!warnings || warnings.length === 0) {
    return null;
  }
  return (
    <ul className="grid gap-1" role="alert">
      {warnings.map((warning) => (
        <li key={warning} className="text-xs leading-[1.35] text-amber-600 dark:text-amber-400">
          {warning}
        </li>
      ))}
    </ul>
  );
}

function CPURow({ cpu }: { cpu: UIRuntimeCPUUsage }): React.ReactElement {
  return (
    <>
      <dt className="font-medium text-foreground">CPU</dt>
      <dd className="text-muted-foreground">
        {cpu.available
          ? `${cpu.utilization ?? ''} of a ${cpu.quota ?? ''} quota`
          : unavailableText(cpu.unavailable)}
      </dd>
    </>
  );
}

function MemoryRows({ memory }: { memory: UIRuntimeMemoryUsage }): React.ReactElement {
  if (!memory.available) {
    return (
      <>
        <dt className="font-medium text-foreground">Memory</dt>
        <dd className="text-muted-foreground">{unavailableText(memory.unavailable)}</dd>
      </>
    );
  }
  const limit = memory.unlimited
    ? 'no limit set'
    : `${memory.limit ?? ''} (${formatPercent(memory.percentOfLimit)})`;
  return (
    <>
      <dt className="font-medium text-foreground">Memory</dt>
      <dd className="text-muted-foreground">
        {memory.current} of {limit}
      </dd>
      <dt className="pl-3 text-muted-foreground">Peak</dt>
      <dd className="text-muted-foreground">{memory.peak}</dd>
      <dt className="pl-3 text-muted-foreground">OOM kills</dt>
      <dd
        className={memory.oomKills > 0 ? 'font-medium text-destructive' : 'text-muted-foreground'}
      >
        {memory.oomKills}
      </dd>
    </>
  );
}

function DiskRow({ disk }: { disk: UIRuntimeDiskUsage }): React.ReactElement {
  return (
    <>
      <dt className="font-medium text-foreground">Disk ({disk.mount})</dt>
      <dd className="text-muted-foreground">
        {disk.available
          ? `${disk.used ?? ''} of ${disk.total ?? ''} (${formatPercent(disk.percentUsed)})`
          : unavailableText(disk.unavailable)}
      </dd>
    </>
  );
}

function unavailableText(reason: string | undefined): string {
  return reason ? `Unavailable — ${reason}` : 'Unavailable';
}

function formatPercent(value: number | undefined): string {
  return `${(value ?? 0).toFixed(0)}%`;
}
