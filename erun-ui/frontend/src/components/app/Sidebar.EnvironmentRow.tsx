import { Download, LoaderCircle, MoreHorizontal } from 'lucide-react';
import * as React from 'react';

import { readError } from '@/app/errors';
import { useAppDispatch, useAppSelector } from '@/app/hooks';
import { openManageDialog } from '@/app/manageEnvironmentThunks';
import { showTerminalMessage } from '@/app/notificationThunks';
import { openOutputs } from '@/app/outputsThunks';
import { closeEnvironment, openSelection } from '@/app/sessionThunks';
import { envKey } from '@/app/slices/sessionsSlice';
import { selectionKey } from '@/app/versionSuggestions';
import { IconTooltip } from '@/components/app/IconTooltip';
import { BusyRowSpinner } from '@/components/app/Sidebar.BusyRowSpinner';
import { EnvHoverCard } from '@/components/app/Sidebar.EnvHoverCard';
import {
  deriveEnvironmentRow,
  type EnvironmentIndicator,
  environmentIndicator,
} from '@/components/app/Sidebar.helpers';
import { StatusDotGlyph } from '@/components/app/Sidebar.StatusDot';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';
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
  const dataEnvState = envState || (indicator.dot === 'busy' ? 'busy' : 'running');
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
            dispatch(showTerminalMessage(readError(error)));
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
  busy,
  busyLabel,
}: {
  environmentName: string;
  rowLabel: string;
  selected: boolean;
  selection: UISelection;
  isLocal: boolean;
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
          dispatch(showTerminalMessage(readError(error)));
        });
      }}
    >
      <span className="min-w-0 truncate">{environmentName}</span>
      {isLocal && <LocalEnvBadge selected={selected} />}
      {busy && <BusyRowSpinner label={busyLabel} />}
    </button>
  );
}

// Each selector returns a primitive so React-Redux equality short-circuits
// row re-renders on unrelated slice churn.
function useEnvironmentRowSelectors(tenantName: string, environmentName: string) {
  const selectedSelection = useAppSelector((state) => state.selection.selected);
  const tenants = useAppSelector((state) => state.tenants.tenants);
  const isOpening = useAppSelector(
    (state) => state.sessions.openingByEnv[envKey(tenantName, environmentName)] === true,
  );
  // First running entry only, so the selector stays primitive-returning and
  // the activity slice's additive churn does not re-render every row.
  const runningCommand = useAppSelector((state) => {
    for (const entry of state.activity.entries) {
      if (
        entry.tenant === tenantName &&
        entry.environment === environmentName &&
        entry.status === 'running'
      ) {
        return entry.command;
      }
    }
    return '';
  });
  const aiBusy = useAppSelector(
    (state) =>
      state.aiActivity.aiBusyByEnv[
        selectionKey({ tenant: tenantName, environment: environmentName })
      ] === true,
  );
  const isOpen = useAppSelector((state) => {
    const key = selectionKey({ tenant: tenantName, environment: environmentName });
    return (state.terminal.tabsByEnv[key]?.length ?? 0) > 0;
  });
  // Scope the busy indicator to THIS env so a reconnect/redeploy in the
  // review pane does not spin or lock the other rows.
  const reconnecting = useAppSelector(
    (state) =>
      state.review.reconnect.status === 'running' &&
      state.review.reconnect.tenant === tenantName &&
      state.review.reconnect.environment === environmentName,
  );
  // The env's real condition behind the open dot: '' running, 'stopped'
  // cloud context down, 'runtime-stopped' runtime scaled to zero, 'failed'
  // deploy or reconnect gave up.
  const envState = useAppSelector(
    (state) =>
      state.envStatus.statusByEnv[
        selectionKey({ tenant: tenantName, environment: environmentName })
      ] ?? '',
  );
  // What the environment itself reports, which is true whoever opened it — the
  // desktop, a CLI `erun open`, or an agent over MCP. Selectors stay primitive-
  // returning so an unchanged observation cannot re-render the row.
  const activityKey = selectionKey({ tenant: tenantName, environment: environmentName });
  const reachable = useAppSelector(
    (state) => state.envStatus.activityByEnv[activityKey]?.reachable === true,
  );
  const envBusy = useAppSelector(
    (state) => state.envStatus.activityByEnv[activityKey]?.busy === true,
  );
  const envBusyDetail = useAppSelector(
    (state) => state.envStatus.activityByEnv[activityKey]?.detail ?? '',
  );
  return {
    selectedSelection,
    tenants,
    isOpening,
    runningCommand,
    aiBusy,
    isOpen,
    reconnecting,
    envState,
    reachable,
    envBusy,
    envBusyDetail,
  };
}

export function EnvironmentRow({
  tenantName,
  environmentName,
}: {
  tenantName: string;
  environmentName: string;
}): React.ReactElement {
  const {
    selectedSelection,
    tenants,
    isOpening,
    runningCommand,
    aiBusy,
    isOpen,
    reconnecting,
    envState,
    reachable,
    envBusy,
    envBusyDetail,
  } = useEnvironmentRowSelectors(tenantName, environmentName);
  const {
    selected: selectedBySelection,
    busy,
    busyLabel,
    isLocal,
    runtimeVersion,
    selection,
  } = deriveEnvironmentRow(
    tenantName,
    environmentName,
    selectedSelection,
    tenants,
    isOpening,
    runningCommand,
    aiBusy,
    reconnecting,
  );
  // While an orchestrator owns the terminal pane, no environment is the focused
  // thing — suppress the env's selected highlight so the sidebar matches what the
  // pane renders (the orchestrator row carries the active highlight instead).
  const orchestratorActive = useAppSelector(
    (state) =>
      state.terminal.sessionId > 0 &&
      state.orchestrators.items.some((o) => o.sessionId === state.terminal.sessionId),
  );
  const selected = selectedBySelection && !orchestratorActive;

  const rowLabel = `${tenantName} / ${environmentName}${isLocal ? ' (local)' : ''}`;
  const indicator = environmentIndicator({
    name: `${tenantName} / ${environmentName}`,
    envState,
    isOpen,
    reachable,
    busy: envBusy,
    detail: envBusyDetail,
  });
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
      runtimeVersion={runtimeVersion}
      activityLabel={busy ? busyLabel : ''}
      indicator={indicator}
    >
      <EnvironmentRowOpenButton
        environmentName={environmentName}
        rowLabel={rowLabel}
        selected={selected}
        selection={selection}
        isLocal={isLocal}
        busy={busy}
        busyLabel={busyLabel}
      />
      {indicator.visible && (
        <EnvStatusIndicator indicator={indicator} selection={selection} envState={envState} />
      )}
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
