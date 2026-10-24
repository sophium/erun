import { Button } from 'erun-kit';
import { RefreshCw, TriangleAlert } from 'lucide-react';
import * as React from 'react';

import { useGetRuntimeUsageQuery } from '@/app/api/environmentApi';
import { cumulativeCPUSeconds } from '@/app/runtimeCPUSeconds';
import { RuntimePanelNotice } from '@/components/app/RuntimePanelNotice';
import { UsageMeter } from '@/components/app/UsageMeter';
import type { UISelection } from '@/types';
import type {
  UIRuntimeCPUUsage,
  UIRuntimeDindUsage,
  UIRuntimeDiskUsage,
  UIRuntimeMemoryUsage,
  UIRuntimeUsage,
} from '@/uiRuntimeTypes';

// Mirror erun-common/runtime_usage.go warn thresholds for meter colour only; the backend's warnings are authoritative.
const MEMORY_WARN_PERCENT = 85;
const MEMORY_PEAK_WARN_PERCENT = 95;
const DISK_WARN_PERCENT = 90;

// RuntimeUsageField shows the env's usage against its own limits.
// An unmeasured field renders as unavailable, never 0, hence `number | undefined`.
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
          <RefreshCw aria-hidden="true" className={isFetching ? 'animate-spin' : undefined} />
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
      <div className="grid gap-2.5">
        <CPUMeter cpu={data.cpu} />
        <MemoryMeters memory={data.memory} />
        {(data.disk ?? []).map((disk) => (
          <DiskMeter key={disk.mount} disk={disk} />
        ))}
      </div>
      <BuildsZone dind={data.dind} excludesBuilds={data.excludesBuilds === true} />
      <RuntimeUsageWarnings warnings={data.warnings} />
    </>
  );
}

const BUILDS_ZONE_CLASS = 'grid gap-2.5 border-t border-border/60 pt-2.5';

// BuildsZone is the erun-dind sidecar's reading, where build work actually runs.
// `excludesBuilds` (set from env type) separates no sidecar from a failed sidecar read.
function BuildsZone({
  dind,
  excludesBuilds,
}: {
  dind: UIRuntimeDindUsage | undefined;
  excludesBuilds: boolean;
}): React.ReactElement | null {
  if (!dind && !excludesBuilds) {
    return null;
  }
  return (
    <div className={BUILDS_ZONE_CLASS}>
      <span className="text-xs leading-[1.35] text-muted-foreground">
        Builds — the erun-dind sidecar every image build runs in
      </span>
      {dind ? (
        <>
          <CPUMeter cpu={dind.cpu} label="Build CPU" />
          <MemoryMeters memory={dind.memory} label="Build memory" />
        </>
      ) : (
        <p className="text-xs leading-[1.35] text-muted-foreground" role="status">
          Not read — the sidecar did not answer, so the figures above exclude its work.
        </p>
      )}
    </div>
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
  if (available) {
    return (
      <p className="text-xs leading-[1.35] text-muted-foreground" role="status">
        {message || 'Open the environment to see its own resource usage.'}
      </p>
    );
  }
  // Shares the Runtime tab's failed-read rendering.
  return (
    <RuntimePanelNotice
      failure={message}
      empty="Open the environment to see its own resource usage."
    />
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
    // overflow-wrap inherits down to the warning text.
    <ul className="grid gap-1 [overflow-wrap:anywhere]" role="alert">
      {warnings.map((warning) => (
        <li
          key={warning}
          className="flex items-start gap-1.5 text-xs leading-[1.35] text-amber-700 dark:text-amber-400"
        >
          <TriangleAlert aria-hidden="true" className="mt-px size-3 shrink-0" />
          <span className="min-w-0">{warning}</span>
        </li>
      ))}
    </ul>
  );
}

function CPUMeter({
  cpu,
  label = 'CPU',
}: {
  cpu: UIRuntimeCPUUsage;
  label?: string;
}): React.ReactElement {
  if (!cpu.available) {
    // Without a cpu.max quota, show cumulative CPU-seconds rather than inventing a rate.
    const seconds = cumulativeCPUSeconds(cpu.usageUsec);
    if (seconds !== null) {
      return (
        <UsageMeter
          label={label}
          valueText={`${String(seconds)} CPU-s`}
          percent={undefined}
          warnAt={undefined}
          detail="cumulative, no CPU quota to measure a rate against"
        />
      );
    }
    return <UnavailableRow label={label} reason={cpu.unavailable} />;
  }
  const quota = cpu.quota ? `of a ${cpu.quota} quota` : 'of an unset quota';
  return (
    <UsageMeter
      label={label}
      valueText={cpu.utilization ?? percentText(cpu.utilizationPercent)}
      percent={cpu.utilizationPercent}
      // CPU has no warn threshold: bursting to quota is normal for a build.
      warnAt={undefined}
      detail={quota}
    />
  );
}

function MemoryMeters({
  memory,
  label = 'Memory',
}: {
  memory: UIRuntimeMemoryUsage;
  label?: string;
}): React.ReactElement {
  if (!memory.available) {
    return <UnavailableRow label={label} reason={memory.unavailable} />;
  }
  if (memory.unlimited) {
    // No ceiling, so no meter.
    return (
      <div className="grid gap-1">
        <div className="flex items-baseline justify-between gap-2">
          <span className="text-xs leading-[1.35] text-muted-foreground">{label}</span>
          <span className="text-sm leading-[1.35] font-semibold tabular-nums text-foreground">
            {memory.current ?? '—'}
          </span>
        </div>
        <span className="text-xs leading-[1.35] text-muted-foreground">no limit set</span>
        <MemoryPeakAndKills memory={memory} />
      </div>
    );
  }
  return (
    <div className="grid gap-1">
      <UsageMeter
        label={label}
        valueText={`${memory.current ?? '—'} of ${memory.limit ?? '—'}`}
        percent={memory.percentOfLimit}
        warnAt={MEMORY_WARN_PERCENT}
        detail={percentDetail(memory.percentOfLimit, 'of the limit')}
      />
      <MemoryPeakAndKills memory={memory} />
    </div>
  );
}

function MemoryPeakAndKills({ memory }: { memory: UIRuntimeMemoryUsage }): React.ReactElement {
  const peakOverThreshold =
    memory.limitBytes !== undefined &&
    memory.peakBytes !== undefined &&
    memory.limitBytes > 0 &&
    (memory.peakBytes / memory.limitBytes) * 100 >= MEMORY_PEAK_WARN_PERCENT;
  const killed = memory.oomKills > 0;
  return (
    <dl className="grid grid-cols-[max-content_1fr] gap-x-2 text-xs leading-[1.35]">
      <dt className="pl-3 text-muted-foreground">Peak</dt>
      <dd
        className={
          peakOverThreshold
            ? 'flex items-center gap-1 font-medium tabular-nums text-amber-700 dark:text-amber-400'
            : 'tabular-nums text-muted-foreground'
        }
      >
        {peakOverThreshold && <TriangleAlert aria-hidden="true" className="size-3 shrink-0" />}
        {memory.peak ?? '—'}
      </dd>
      <dt className="pl-3 text-muted-foreground">OOM kills</dt>
      <dd
        className={
          killed
            ? 'flex items-center gap-1 font-medium tabular-nums text-destructive'
            : 'tabular-nums text-muted-foreground'
        }
      >
        {killed && <TriangleAlert aria-hidden="true" className="size-3 shrink-0" />}
        {memory.oomKills}
      </dd>
    </dl>
  );
}

function DiskMeter({ disk }: { disk: UIRuntimeDiskUsage }): React.ReactElement {
  if (!disk.available) {
    return <UnavailableRow label={`Disk (${disk.mount})`} reason={disk.unavailable} />;
  }
  return (
    <UsageMeter
      label={`Disk (${disk.mount})`}
      valueText={`${disk.used ?? '—'} of ${disk.total ?? '—'}`}
      percent={disk.percentUsed}
      warnAt={DISK_WARN_PERCENT}
      detail={percentDetail(disk.percentUsed, 'used')}
    />
  );
}

// Parallel to a meter row so the panel does not reflow.
function UnavailableRow({
  label,
  reason,
}: {
  label: string;
  reason: string | undefined;
}): React.ReactElement {
  return (
    <div className="flex items-baseline justify-between gap-2">
      <span className="text-xs leading-[1.35] text-muted-foreground">{label}</span>
      <span className="text-xs leading-[1.35] text-muted-foreground italic">
        {reason ? `Unavailable — ${reason}` : 'Unavailable'}
      </span>
    </div>
  );
}

// Never substitute 0% for a missing reading.
function percentText(value: number | undefined): string {
  return value === undefined || !Number.isFinite(value) ? '—' : `${value.toFixed(0)}%`;
}

// Memory is "of the limit"; a disk mount is just "used".
function percentDetail(value: number | undefined, qualifier: string): string | undefined {
  return value === undefined || !Number.isFinite(value)
    ? undefined
    : `${value.toFixed(0)}% ${qualifier}`;
}
