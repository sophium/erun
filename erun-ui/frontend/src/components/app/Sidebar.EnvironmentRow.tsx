import { Download, LoaderCircle, MoreHorizontal, TriangleAlert } from 'lucide-react';
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
import { EnvHoverCard } from '@/components/app/Sidebar.EnvHoverCard';
import { deriveEnvironmentRow } from '@/components/app/Sidebar.helpers';
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
          void dispatch(openOutputs(selection));
        }}
      >
        <Download />
      </Button>
    </IconTooltip>
  );
}

function BusyRowSpinner({ label }: { label: string }): React.ReactElement {
  return (
    <LoaderCircle
      className="size-3.5 flex-none animate-spin text-current opacity-75"
      aria-label={label || undefined}
      aria-hidden={label ? undefined : true}
      role={label ? 'status' : undefined}
    />
  );
}

// OpenEnvDot renders the open indicator next to an env name when the env has
// live tabs registered (the user clicked the row at least once and
// Local/ERun/AI tabs were spawned). Its shape and colour reflect the env's
// REAL condition (tab presence alone is not running-ness):
// green filled circle while running, a hollow grey ring while the linked
// cloud context is stopped, an amber triangle after a failed deploy /
// abandoned reconnect. Shape + accessible label carry the state, never
// colour alone. The dot is a real button so clicking it tears the tabs down
// via the closeEnvironment thunk; stopPropagation keeps the click from
// bubbling to the row's openSelection handler. Distinct from the LOCAL pill
// (which marks the row as the dev-machine env) and from BusyRowSpinner
// (which fires only while an activity-queue entry is running) — open and
// busy are independent states and can be visible together.
function OpenEnvDot({
  tenantName,
  environmentName,
  selection,
  envState,
}: {
  tenantName: string;
  environmentName: string;
  selection: UISelection;
  envState: string;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  const name = `${tenantName} / ${environmentName}`;
  const closeHint = `Click to close its tabs — terminals + tracking only, leaves AWS untouched`;
  let label = `Close ${name}`;
  let tooltip = `${label} — terminals + tracking only, leaves AWS untouched`;
  let glyph = (
    <span
      aria-hidden="true"
      className="block size-2 rounded-full bg-emerald-500 shadow-[0_0_0_1px_color-mix(in_oklch,currentColor_20%,transparent)]"
    />
  );
  if (envState === 'stopped') {
    label = `${name} is stopped — start it from the titlebar`;
    tooltip = `${label}. ${closeHint}`;
    glyph = (
      <span
        aria-hidden="true"
        className="block size-2 rounded-full border-[1.5px] border-muted-foreground bg-transparent"
      />
    );
  } else if (envState === 'failed') {
    label = `${name} deploy failed — recover from Activities or re-click the row`;
    tooltip = `${label}. ${closeHint}`;
    glyph = <TriangleAlert aria-hidden="true" className="size-2.5 text-amber-500" />;
  }
  return (
    <IconTooltip label={tooltip}>
      <Button
        type="button"
        variant="ghost"
        size="icon"
        className="size-[18px] flex-none cursor-pointer rounded-full border-0 bg-transparent p-0 text-current hover:bg-[color-mix(in_oklch,currentColor_12%,transparent)]"
        aria-label={label}
        data-testid="env-open-dot"
        data-env-state={envState || 'running'}
        onClick={(event) => {
          event.stopPropagation();
          void dispatch(closeEnvironment(selection)).catch((error: unknown) => {
            dispatch(showTerminalMessage(readError(error)));
          });
        }}
      >
        {glyph}
      </Button>
    </IconTooltip>
  );
}

// useEnvironmentRowSelectors batches the per-env state reads the sidebar
// row needs. Each selector returns a primitive (or a memoised array) so
// React-Redux's default equality short-circuits row re-renders when
// unrelated slice churn happens.
function useEnvironmentRowSelectors(tenantName: string, environmentName: string) {
  const selectedSelection = useAppSelector((state) => state.selection.selected);
  const tenants = useAppSelector((state) => state.tenants.tenants);
  const isOpening = useAppSelector(
    (state) => state.sessions.openingByEnv[envKey(tenantName, environmentName)] === true,
  );
  // runningCommand is the first 'running' activity command attached to
  // this env (deploy / init / sshd-init / doctor / build / push /
  // release). Picking the first entry keeps the selector primitive-
  // returning so the activity slice's additive churn does not re-render
  // every row.
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
  // isOpen = the user has opened this env at least once and at least
  // one of its tabs is still alive.
  const isOpen = useAppSelector((state) => {
    const key = selectionKey({ tenant: tenantName, environment: environmentName });
    return (state.terminal.tabsByEnv[key]?.length ?? 0) > 0;
  });
  // reconnecting flips the row's busy indicator while the review-pane
  // reconnect/redeploy is in flight for THIS env. Other rows stay
  // interactive and unspinning (Nielsen #1 visibility of system status
  // without blocking Nielsen #3 user control & freedom).
  const reconnecting = useAppSelector(
    (state) =>
      state.review.reconnect.status === 'running' &&
      state.review.reconnect.tenant === tenantName &&
      state.review.reconnect.environment === environmentName,
  );
  // envState is the env's real condition behind the open dot:
  // '' (running/normal), 'stopped' (cloud context not running), 'failed'
  // (deploy failed / reconnect gave up). Driven by the env-status event.
  const envState = useAppSelector(
    (state) =>
      state.envStatus.statusByEnv[
        selectionKey({ tenant: tenantName, environment: environmentName })
      ] ?? '',
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
  };
}

export function EnvironmentRow({
  tenantName,
  environmentName,
}: {
  tenantName: string;
  environmentName: string;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  const {
    selectedSelection,
    tenants,
    isOpening,
    runningCommand,
    aiBusy,
    isOpen,
    reconnecting,
    envState,
  } = useEnvironmentRowSelectors(tenantName, environmentName);
  const { selected, busy, busyLabel, isLocal, runtimeVersion, selection } = deriveEnvironmentRow(
    tenantName,
    environmentName,
    selectedSelection,
    tenants,
    isOpening,
    runningCommand,
    aiBusy,
    reconnecting,
  );

  const rowLabel = `${tenantName} / ${environmentName}${isLocal ? ' (local)' : ''}`;
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
      isOpen={isOpen}
      envState={envState}
    >
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
      {isOpen && (
        <OpenEnvDot
          tenantName={tenantName}
          environmentName={environmentName}
          selection={selection}
          envState={envState}
        />
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

// PendingEnvironmentRow renders an optimistic, non-interactive
// placeholder row for an environment that is currently being created
// by `erun init`. It exists to satisfy Nielsen #1 (visibility of
// system status) for the ~1–2 min init runs: without it,
// state.selected is set but produces no visible affordance because
// the env is not in state.tenants yet. Italic name + "Creating"
// badge + spinner + aria-busy communicate the in-flight state
// without inviting interaction.
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
