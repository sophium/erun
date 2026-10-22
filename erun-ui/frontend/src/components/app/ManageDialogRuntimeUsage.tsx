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

// These mirror the named thresholds the reader itself branches on --
// eruncommon.RuntimeUsageMemoryWarnPercent / MemoryPeakWarnPercent /
// DiskWarnPercent (erun-common/runtime_usage.go). They are duplicated here
// only so a crossing can colour its own meter at a glance; the authoritative
// crossing is still the backend's own `warnings` list, which is rendered
// below and is what an operator should act on. Keep them in step with the Go
// constants -- a drift shows up as a bar that disagrees with the warning
// beside it.
const MEMORY_WARN_PERCENT = 85;
const MEMORY_PEAK_WARN_PERCENT = 95;
const DISK_WARN_PERCENT = 90;

// RuntimeUsageField answers "how close is this environment to its own
// limits" right beside the sliders that move those limits. Unlike
// RuntimeResourceControls (a reading of the node), this is the environment's
// own opinion: CPU against its quota, memory current/peak against its own
// cgroup limit with the real OOM-kill count, and disk on the workspace mount.
//
// Fail-soft by contract (Nielsen #1, visibility of system status): a field
// the reader could not measure (cgroup v1, an unlimited limit, an unreadable
// file) renders its own "unavailable" state, never a confident 0 or 0%, since
// that would read as "idle" or "empty" rather than "unknown". That contract is
// why percentages are passed around as `number | undefined` rather than being
// defaulted at the formatting boundary.
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

// BuildsZone is the erun-dind sidecar's own reading, in its own zone under the
// runtime container's. On a build-capable environment the figures above are
// near-idle by construction — a release lane spends its time waiting on bounded
// `erun exec job await` calls, so the CPU is near zero whether the build is
// healthy or wedged — and this is the container the work is actually in. The
// heading is not decoration: without it the operator has two CPU figures and no
// way to tell which domain each belongs to, which is the whole defect.
//
// An absent reading is likewise two states, and only one of them is no zone.
// An environment that carries no sidecar has nothing to show here; an
// environment that carries one and whose reading failed gets the heading and
// says so, because the alternative is the near-idle figures above qualified
// only by the reader's own "excluding builds" prose — a statement that reads as
// "idle" rather than as "unknown", which is the opposite of what it means.
// `excludesBuilds` is what separates the two, and the reader sets it from the
// environment's own type rather than from whether the sidecar answered.
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
  // An unreachable pod explains itself rather than rendering an empty panel
  // that reads as "nothing to see", through the Runtime tab's one rendering
  // for a failed read so this panel cannot drift from its neighbours.
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
    // Each row already carries its own glyph, so what is missing is the wrap:
    // `overflow-wrap` inherits from the list down to the warning text, which is
    // where a threshold crossing names its number and its limit.
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

// cpu is the runtime container's own reading by default, and the erun-dind
// sidecar's when the Builds block passes its own label; nothing else differs,
// so the same meter renders both.
function CPUMeter({
  cpu,
  label = 'CPU',
}: {
  cpu: UIRuntimeCPUUsage;
  label?: string;
}): React.ReactElement {
  if (!cpu.available) {
    // A container with no cpu.max quota cannot report utilisation — but it can
    // report what it has done, and that is the common shape of the sidecar
    // (declared without a limit so a build can use the node). Rendering the
    // cumulative figure keeps a busy build from reading as an unavailable one;
    // it is stated as CPU-seconds precisely because it is NOT a rate, and a
    // "386 CPU-s of no quota" percentage-shaped line would invent the rate.
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
      // CPU has no named warn threshold in erun-common -- bursting to the
      // quota is normal for a build -- so the meter shows magnitude only.
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
    // A real reading, not a failure: there is no ceiling to be a fraction of,
    // so a meter would have nothing to fill against.
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

// Peak and OOM kills are evidence for the same slider decision as the current
// figure, so they sit under it rather than as sibling rows -- but subordinate,
// because the current reading is what the operator came for.
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

// A stated unavailability, never a zero. Kept visually parallel to a meter row
// so the panel does not reflow into a different shape when one field cannot be
// read -- the label stays where the eye expects it.
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

// percentText and percentDetail never substitute a 0 for a missing reading:
// "0%" is a measurement, and rendering one where none was taken is exactly the
// confident-wrong-number failure this panel exists to avoid.
function percentText(value: number | undefined): string {
  return value === undefined || !Number.isFinite(value) ? '—' : `${value.toFixed(0)}%`;
}

// The qualifier is the caller's, because the noun differs by resource and the
// wrong one is user-facing nonsense: memory is a fraction "of the limit" it was
// given, while a disk mount has capacity, not a limit, and is simply "used".
function percentDetail(value: number | undefined, qualifier: string): string | undefined {
  return value === undefined || !Number.isFinite(value)
    ? undefined
    : `${value.toFixed(0)}% ${qualifier}`;
}
