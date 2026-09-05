import { Button, cn, IconTooltip } from 'erun-kit';
import { Download, LoaderCircle, MoreHorizontal } from 'lucide-react';
import * as React from 'react';

import { readError } from '@/app/errors';
import { useAppDispatch } from '@/app/hooks';
import { openManageDialog } from '@/app/manageEnvironmentThunks';
import { showTerminalError } from '@/app/notificationThunks';
import { openOutputs } from '@/app/outputsThunks';
import { closeEnvironment, openSelection } from '@/app/sessionThunks';
import { BusyRowSpinner } from '@/components/app/Sidebar.BusyRowSpinner';
import { EnvHoverCard } from '@/components/app/Sidebar.EnvHoverCard';
import { useEnvironmentRowState } from '@/components/app/Sidebar.EnvironmentRow.state';
import {
  environmentCardActivityLabel,
  type EnvironmentIndicator,
} from '@/components/app/Sidebar.helpers';
import { NodeStateIndicator } from '@/components/app/Sidebar.NodeStateIndicator';
import { StatusDotGlyph } from '@/components/app/Sidebar.StatusDot';
import type { UISelection } from '@/types';

function LocalEnvBadge({ selected }: { selected: boolean }): React.ReactElement {
  return (
    <span
      className={cn(
        'flex-none rounded-[calc(var(--radius)-4px)] border px-1 py-px text-[10px] font-medium uppercase leading-none tracking-wide',
        selected
          ? 'border-primary-foreground/40 text-primary-foreground/85'
          : 'border-border text-muted-foreground',
      )}
      aria-label="Local environment"
    >
      Local
    </span>
  );
}

// HostEnvBadge marks a host environment distinctly from a local-agent one: it
// has no pod and no cluster at all, so it must never be presented as a pod
// that failed to start.
function HostEnvBadge({ selected }: { selected: boolean }): React.ReactElement {
  return (
    <span
      className={cn(
        'flex-none rounded-[calc(var(--radius)-4px)] border px-1 py-px text-[10px] font-medium uppercase leading-none tracking-wide',
        selected
          ? 'border-primary-foreground/40 text-primary-foreground/85'
          : 'border-border text-muted-foreground',
      )}
      aria-label="Host environment — no pod, this machine only"
    >
      Host
    </span>
  );
}

function EnvironmentRowEditButton({
  tenantName,
  environmentName,
  selected,
  selection,
}: {
  tenantName: string;
  environmentName: string;
  selected: boolean;
  selection: UISelection;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  return (
    <IconTooltip label="Edit environment settings">
      <Button
        type="button"
        className={cn(
          'pointer-events-none size-[26px] flex-none cursor-pointer border-0 bg-transparent text-current opacity-0 transition-[opacity,background-color,color] duration-150 hover:bg-[color-mix(in_oklch,currentColor_12%,transparent)] hover:text-current group-hover:pointer-events-auto group-hover:opacity-100 group-focus-within:pointer-events-auto group-focus-within:opacity-100 [&_svg]:size-4',
          selected && 'pointer-events-auto opacity-100',
        )}
        variant="ghost"
        size="icon"
        aria-label={`Edit ${tenantName} / ${environmentName} settings`}
        onClick={(event) => {
          event.stopPropagation();
          dispatch(openManageDialog(selection));
        }}
      >
        <MoreHorizontal />
      </Button>
    </IconTooltip>
  );
}

function EnvironmentRowOutputsButton({
  tenantName,
  environmentName,
  selected,
  selection,
}: {
  tenantName: string;
  environmentName: string;
  selected: boolean;
  selection: UISelection;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  return (
    <IconTooltip label="View and download agent outputs">
      <Button
        type="button"
        className={cn(
          'pointer-events-none size-[26px] flex-none cursor-pointer border-0 bg-transparent text-current opacity-0 transition-[opacity,background-color,color] duration-150 hover:bg-[color-mix(in_oklch,currentColor_12%,transparent)] hover:text-current group-hover:pointer-events-auto group-hover:opacity-100 group-focus-within:pointer-events-auto group-focus-within:opacity-100 [&_svg]:size-4',
          selected && 'pointer-events-auto opacity-100',
        )}
        variant="ghost"
        size="icon"
        aria-label={`Outputs for ${tenantName} / ${environmentName}`}
        onClick={(event) => {
          event.stopPropagation();
          void dispatch(openOutputs({ kind: 'environment', selection }));
        }}
      >
        <Download />
      </Button>
    </IconTooltip>
  );
}

// The indicator's shape carries the env's real condition (running / busy /
// stopped / failed), not mere tab presence — open is not running, and an env in
// use from the CLI is running without being open here. Conveyed via shape +
// label, never colour alone. Independent of the busy spinner, which reports the
// desktop's own in-flight command; both can show at once.
function EnvStatusIndicator({
  indicator,
  selection,
  envState,
}: {
  indicator: EnvironmentIndicator;
  selection: UISelection;
  envState: string;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  // Fall back to the derived indicator, not to a literal: a condition the
  // environment reports rather than the desktop (a broken port-forward) has no
  // envState, and hard-coding "running" for it would leave the attribute
  // contradicting the glyph beside it.
  const dataEnvState = envState || indicator.dot;
  // Not opened here means there are no tabs to close, so the indicator is a
  // passive status light rather than a control that would do nothing.
  if (!indicator.opened) {
    return (
      <IconTooltip label={indicator.condition}>
        <span
          role="img"
          aria-label={indicator.condition}
          data-testid="env-open-dot"
          data-env-state={dataEnvState}
          data-env-opened="false"
          className="flex size-[18px] flex-none items-center justify-center rounded-full text-current"
        >
          <StatusDotGlyph state={indicator.dot} />
        </span>
      </IconTooltip>
    );
  }
  const closeHint = 'Click to close its tabs — terminals + tracking only, leaves AWS untouched';
  const isPlainRunning = indicator.dot === 'running';
  const label = isPlainRunning
    ? `Close ${selection.tenant} / ${selection.environment}`
    : indicator.condition;
  const tooltip = isPlainRunning
    ? `${label} — terminals + tracking only, leaves AWS untouched`
    : `${indicator.condition}. ${closeHint}`;
  return (
    <IconTooltip label={tooltip}>
      <Button
        type="button"
        variant="ghost"
        size="icon"
        className="size-[18px] flex-none cursor-pointer rounded-full border-0 bg-transparent p-0 text-current hover:bg-[color-mix(in_oklch,currentColor_12%,transparent)]"
        aria-label={label}
        data-testid="env-open-dot"
        data-env-state={dataEnvState}
        data-env-opened="true"
        onClick={(event) => {
          event.stopPropagation();
          void dispatch(closeEnvironment(selection)).catch((error: unknown) => {
            dispatch(showTerminalError(readError(error)));
          });
        }}
      >
        <StatusDotGlyph state={indicator.dot} />
      </Button>
    </IconTooltip>
  );
}

// The row's main click target: selecting the environment opens it.
function EnvironmentRowOpenButton({
  environmentName,
  rowLabel,
  selected,
  selection,
  isLocal,
  isHost,
  busy,
  busyLabel,
}: {
  environmentName: string;
  rowLabel: string;
  selected: boolean;
  selection: UISelection;
  isLocal: boolean;
  isHost: boolean;
  busy: boolean;
  busyLabel: string;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  return (
    <button
      type="button"
      className={cn(
        'flex h-8 min-w-0 flex-1 cursor-pointer items-center gap-1.5 border-0 bg-transparent py-0 pr-2 pl-10 text-left text-sm leading-[1.2] tracking-normal text-inherit',
        selected ? 'font-medium' : 'font-normal',
      )}
      aria-label={rowLabel}
      aria-current={selected ? 'page' : undefined}
      onClick={() => {
        void dispatch(openSelection(selection)).catch((error: unknown) => {
          dispatch(showTerminalError(readError(error)));
        });
      }}
    >
      <span className="min-w-0 truncate">{environmentName}</span>
      {isHost ? (
        <HostEnvBadge selected={selected} />
      ) : (
        isLocal && <LocalEnvBadge selected={selected} />
      )}
      {busy && <BusyRowSpinner label={busyLabel} />}
    </button>
  );
}

export function EnvironmentRow({
  tenantName,
  environmentName,
}: {
  tenantName: string;
  environmentName: string;
}): React.ReactElement {
  const {
    selected,
    busy,
    busyLabel,
    busyFromEnvironment,
    isLocal,
    isHost,
    usageExcludesBuilds,
    runtimeVersion,
    runtimeVersionLine,
    erunVersion,
    runtimeImageLineMismatch,
    selection,
    envState,
    indicator,
    nodeIndicator,
    usage,
    node,
  } = useEnvironmentRowState(tenantName, environmentName);
  const rowLabel = `${tenantName} / ${environmentName}${isHost ? ' (host)' : isLocal ? ' (local)' : ''}`;
  return (
    <EnvHoverCard
      className={cn(
        'group relative mr-1 ml-1 flex h-8 items-center rounded-md pr-1.5 text-foreground hover:bg-sidebar-accent hover:text-sidebar-accent-foreground',
        selected &&
          'bg-primary text-primary-foreground shadow-sm hover:bg-primary hover:text-primary-foreground',
      )}
      tenantName={tenantName}
      environmentName={environmentName}
      selection={selection}
      isLocal={isLocal}
      isHost={isHost}
      runtimeVersion={runtimeVersion}
      runtimeVersionLine={runtimeVersionLine}
      erunVersion={erunVersion}
      runtimeImageLineMismatch={runtimeImageLineMismatch}
      activityLabel={environmentCardActivityLabel(
        busy,
        busyFromEnvironment,
        busyLabel,
        indicator.dot,
      )}
      indicator={indicator}
      nodeIndicator={nodeIndicator}
      node={node}
      usage={usage}
      usageExcludesBuilds={usageExcludesBuilds}
    >
      <EnvironmentRowOpenButton
        environmentName={environmentName}
        rowLabel={rowLabel}
        selected={selected}
        selection={selection}
        isLocal={isLocal}
        isHost={isHost}
        busy={busy}
        busyLabel={busyLabel}
      />
      {/* A host env has no pod, so it is never "running" or "stopped" — it
          simply is. Showing the pod-shaped open/close status dot for it would
          present a directory as a pod that failed to start. */}
      {!isHost && indicator.visible && (
        <EnvStatusIndicator indicator={indicator} selection={selection} envState={envState} />
      )}
      {/* A second indicator, about the machine rather than the environment. It
          is what keeps a row from rendering as "nothing to say" when the one
          thing that IS known about it is that its node is down. */}
      {!isHost && <NodeStateIndicator indicator={nodeIndicator} />}
      <EnvironmentRowOutputsButton
        tenantName={tenantName}
        environmentName={environmentName}
        selected={selected}
        selection={selection}
      />
      <EnvironmentRowEditButton
        tenantName={tenantName}
        environmentName={environmentName}
        selected={selected}
        selection={selection}
      />
    </EnvHoverCard>
  );
}

// PendingEnvironmentRow is an optimistic placeholder for an env being created
// by `erun init`: during the ~1–2 min run state.selected points at an env not
// yet in state.tenants, so without this row the selection has no visible
// affordance.
export function PendingEnvironmentRow({
  tenantName,
  environmentName,
}: {
  tenantName: string;
  environmentName: string;
}): React.ReactElement {
  return (
    <div
      className="group relative mr-1 ml-1 flex h-8 items-center rounded-md pr-1.5 text-muted-foreground"
      aria-busy="true"
      aria-live="polite"
      aria-label={`Creating ${tenantName} / ${environmentName}`}
    >
      <div className="flex h-8 min-w-0 flex-1 items-center gap-1.5 py-0 pr-2 pl-10 text-left text-sm leading-[1.2]">
        <span className="min-w-0 truncate italic">{environmentName}</span>
        <span
          className="flex-none rounded-[calc(var(--radius)-4px)] border border-border px-1 py-px text-[10px] font-medium uppercase leading-none tracking-wide text-muted-foreground"
          aria-hidden="true"
        >
          Creating
        </span>
        <LoaderCircle
          className="size-3.5 flex-none animate-spin text-current opacity-75"
          aria-hidden="true"
        />
      </div>
    </div>
  );
}
