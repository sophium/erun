import {
  BookOpen,
  Bot,
  Download,
  MoreHorizontal,
  Plus,
  RefreshCw,
  Settings,
  Stethoscope,
} from 'lucide-react';
import * as React from 'react';

import { openDocumentation } from '@/app/documentationThunks';
import { openGlobalConfigDialog } from '@/app/globalConfigThunks';
import { useAppDispatch, useAppSelector } from '@/app/hooks';
import { startManageDoctor } from '@/app/manageEnvironmentThunks';
import { orchestratorShellLabel } from '@/app/orchestratorShellLabel';
import {
  loadOrchestrators,
  openOrchestrator,
  restartApp,
  startOrchestrator,
} from '@/app/orchestratorThunks';
import { openOutputs } from '@/app/outputsThunks';
import { selectSidebarFocus } from '@/app/selectors';
import type { OrchestratorShellActivity } from '@/app/slices/orchestratorShellActivitySlice';
import type { OrchestratorInfo } from '@/app/slices/orchestratorsSlice';
import { openOrchestratorDialog } from '@/app/slices/orchestratorsSlice';
import { EmptyState } from '@/components/app/EmptyState';
import { IconTooltip } from '@/components/app/IconTooltip';
import { BusyRowSpinner } from '@/components/app/Sidebar.BusyRowSpinner';
import { StatusDotGlyph } from '@/components/app/Sidebar.StatusDot';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';

// ErunSection is the top-level "ERUN" sidebar block above ENVIRONMENTS: the
// operator's host-side control plane. Its headline is the cross-env AI
// orchestrators — host-side AI sessions that drive environments (delegate work to
// in-pod agents, review the synced mirror, run built artifacts on this machine),
// grouped by tenant — and its header carries the erun-global actions.
export function ErunSection(): React.ReactElement {
  return (
    <div className="pb-3" data-testid="erun-section">
      <ErunHeader />
      <OrchestratorsArea />
    </div>
  );
}

function ErunHeader(): React.ReactElement {
  const dispatch = useAppDispatch();
  // The orchestrator to return to after a restart is whichever one owns the
  // active terminal session, if any.
  const activeOrchestratorId = useAppSelector((state) => {
    const activeSessionId = state.terminal.sessionId;
    if (activeSessionId <= 0) {
      return '';
    }
    return state.orchestrators.items.find((o) => o.sessionId === activeSessionId)?.id ?? '';
  });
  // Doctor diagnoses one environment's runtime and config, so it needs a
  // target; disable rather than silently no-op when nothing is selected
  // (Nielsen #5, error prevention).
  const hasSelectedEnvironment = useAppSelector((state) => state.selection.selected !== null);
  return (
    <div className="flex items-center justify-between gap-2 pr-1.5 pb-1.5 pl-3.5">
      <span className="text-xs leading-[1.2] font-semibold tracking-normal text-muted-foreground uppercase">
        Erun
      </span>
      <div className="flex items-center gap-1">
        <IconTooltip label="Restart app & return to this orchestrator">
          <Button
            className="size-[26px] flex-none text-muted-foreground hover:bg-sidebar-accent hover:text-sidebar-accent-foreground [&_svg]:size-4"
            type="button"
            variant="ghost"
            size="icon-xs"
            aria-label="Restart app"
            onClick={() => {
              void dispatch(restartApp(activeOrchestratorId));
            }}
          >
            <RefreshCw />
          </Button>
        </IconTooltip>
        <IconTooltip
          label={hasSelectedEnvironment ? 'Run doctor' : 'Select an environment to run doctor'}
        >
          <Button
            className="size-[26px] flex-none text-muted-foreground hover:bg-sidebar-accent hover:text-sidebar-accent-foreground [&_svg]:size-4"
            type="button"
            variant="ghost"
            size="icon-xs"
            aria-label="Run doctor"
            disabled={!hasSelectedEnvironment}
            onClick={() => {
              void dispatch(startManageDoctor());
            }}
          >
            <Stethoscope />
          </Button>
        </IconTooltip>
        <IconTooltip label="Open ERun settings">
          <Button
            className="size-[26px] flex-none text-muted-foreground hover:bg-sidebar-accent hover:text-sidebar-accent-foreground [&_svg]:size-4"
            type="button"
            variant="ghost"
            size="icon-xs"
            aria-label="Open ERun settings"
            onClick={() => {
              dispatch(openGlobalConfigDialog());
            }}
          >
            <Settings />
          </Button>
        </IconTooltip>
        <IconTooltip label="Open documentation">
          <Button
            className="size-[26px] flex-none text-muted-foreground hover:bg-sidebar-accent hover:text-sidebar-accent-foreground [&_svg]:size-4"
            type="button"
            variant="ghost"
            size="icon-xs"
            aria-label="Open documentation"
            onClick={() => {
              dispatch(openDocumentation());
            }}
          >
            <BookOpen />
          </Button>
        </IconTooltip>
      </div>
    </div>
  );
}

function OrchestratorsArea(): React.ReactElement {
  const dispatch = useAppDispatch();
  const items = useAppSelector((state) => state.orchestrators.items);
  const error = useAppSelector((state) => state.orchestrators.error);
  const focus = useAppSelector(selectSidebarFocus);

  // The orchestrator a rebuild+restart returns to is restored by boot(), which
  // owns the initial pane selection; here we only load the list for the sidebar.
  React.useEffect(() => {
    void dispatch(loadOrchestrators());
  }, [dispatch]);

  return (
    <div>
      <div className="flex items-center justify-between gap-2 pr-1.5 pl-3.5">
        <span className="pt-2 pb-1 text-[10px] font-medium tracking-wide text-muted-foreground/80 uppercase">
          AI Orchestrators
        </span>
        <IconTooltip label="New orchestrator">
          <Button
            type="button"
            variant="ghost"
            size="icon-xs"
            className="size-[26px] flex-none text-muted-foreground hover:bg-sidebar-accent hover:text-sidebar-accent-foreground [&_svg]:size-4"
            aria-label="New orchestrator"
            onClick={() => {
              dispatch(openOrchestratorDialog(null));
            }}
          >
            <Plus />
          </Button>
        </IconTooltip>
      </div>
      {items.length === 0 ? (
        <div className="px-3.5 pb-1">
          <EmptyState
            icon={<Bot />}
            heading="No orchestrators yet"
            body="Host-side AI sessions that drive every environment — delegate work to the in-pod agents, review the synced mirror, run built artifacts here."
          />
        </div>
      ) : (
        <ul aria-label="AI orchestrators">
          {items.map((orchestrator) => (
            <OrchestratorRow
              key={orchestrator.id}
              orchestrator={orchestrator}
              active={focus.kind === 'orchestrator' && focus.sessionId === orchestrator.sessionId}
            />
          ))}
        </ul>
      )}
      {error ? (
        <p role="alert" className="px-3.5 pb-1 text-[11px] break-words text-destructive">
          {error}
        </p>
      ) : null}
    </div>
  );
}

function OrchestratorRow({
  orchestrator,
  active,
}: {
  orchestrator: OrchestratorInfo;
  active: boolean;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  const running = orchestrator.status === 'running' && orchestrator.sessionId > 0;
  // Scoped to this orchestrator's own session, so concurrent orchestrators each
  // spin on their own row rather than on whichever one happens to be selected.
  const busy = useAppSelector(
    (state) =>
      orchestrator.sessionId > 0 &&
      state.aiActivity.aiBusyBySession[orchestrator.sessionId] === true,
  );
  // A background shell is independent of the turn's own busy state —
  // it can keep running after the turn that started it goes idle, which is
  // exactly the case that had no affordance at all before this. Shown only
  // when the turn itself is not already spinning, so the row never carries
  // two motion cues fighting for the same attention.
  const shellActivity = useAppSelector((state) =>
    orchestrator.sessionId > 0
      ? state.orchestratorShellActivity.bySession[orchestrator.sessionId]
      : undefined,
  );
  return (
    <li
      className={cn(
        'group mr-1 ml-1 flex h-8 items-center gap-1.5 rounded-md pr-1.5 pl-3.5 text-foreground hover:bg-sidebar-accent hover:text-sidebar-accent-foreground',
        active &&
          'bg-primary text-primary-foreground shadow-sm hover:bg-primary hover:text-primary-foreground',
      )}
    >
      <button
        type="button"
        className={cn(
          'flex min-w-0 flex-1 cursor-pointer items-center gap-1.5 border-0 bg-transparent py-0 text-left text-sm leading-[1.2] tracking-normal text-inherit',
          active ? 'font-medium' : 'font-normal',
        )}
        aria-label={`${running ? 'Open' : 'Start'} orchestrator ${orchestrator.name}`}
        aria-current={active ? 'page' : undefined}
        onClick={() => {
          if (running) {
            dispatch(openOrchestrator(orchestrator.sessionId));
          } else {
            void dispatch(startOrchestrator(orchestrator.id));
          }
        }}
      >
        <span className="min-w-0 truncate">{orchestrator.name}</span>
      </button>
      {busy && <OrchestratorBusyIndicator name={orchestrator.name} />}
      {!busy && shellActivity?.running && (
        <OrchestratorShellIndicator name={orchestrator.name} activity={shellActivity} />
      )}
      <OrchestratorRowActions orchestrator={orchestrator} running={running} active={active} />
    </li>
  );
}

// OrchestratorBusyIndicator is the turn-busy sibling of OrchestratorShellIndicator
// below, wrapped in the same IconTooltip so a working orchestrator explains
// itself on hover exactly like every other spinner in the sidebar — the
// aria-label alone reached only screen readers, leaving a sighted mouse user
// with an inert spin.
function OrchestratorBusyIndicator({ name }: { name: string }): React.ReactElement {
  const label = `${name} is working`;
  return (
    <IconTooltip label={label}>
      <BusyRowSpinner label={label} />
    </IconTooltip>
  );
}

// OrchestratorShellIndicator is BusyRowSpinner's own icon, reused rather than
// a one-off, so a running shell reports activity identically to a working
// turn or a busy environment (Nielsen #4, consistency). The aria-label — and
// the hover tooltip, since elapsed time and the command are worth seeing at a
// glance, not just to a screen reader — name what is running and for how
// long, which is the whole point of this indicator: "1 shell" alone cannot
// tell the operator whether it is doing something or just spinning its wheels.
function OrchestratorShellIndicator({
  name,
  activity,
}: {
  name: string;
  activity: OrchestratorShellActivity;
}): React.ReactElement {
  const label = orchestratorShellLabel(name, activity.command, activity.startedAtUnix, Date.now());
  return (
    <IconTooltip label={label}>
      <BusyRowSpinner label={label} />
    </IconTooltip>
  );
}

// The orchestrator row mirrors the environment row: a shape-encoded status dot
// plus a single hover-revealed "…" that opens the orchestrator's dialog, where
// restart and delete live. Clicking the row itself starts or opens the
// orchestrator, so no inline start/stop button is needed. Transient
// (Investigate) orchestrators have no persisted definition to manage, so they
// show only the status dot.
function OrchestratorRowActions({
  orchestrator,
  running,
  active,
}: {
  orchestrator: OrchestratorInfo;
  running: boolean;
  active: boolean;
}): React.ReactElement {
  return (
    <>
      <span
        className="flex size-[18px] flex-none items-center justify-center"
        role="img"
        aria-label={
          running
            ? `Orchestrator ${orchestrator.name} is running`
            : `Orchestrator ${orchestrator.name} is stopped`
        }
      >
        <StatusDotGlyph state={running ? 'running' : 'stopped'} />
      </span>
      {!orchestrator.transient && (
        <>
          <OrchestratorRowOutputsButton orchestrator={orchestrator} active={active} />
          <OrchestratorRowDetailsButton orchestrator={orchestrator} active={active} />
        </>
      )}
    </>
  );
}

// The environment row has this same affordance for the files its agent produced
// in the pod. An orchestrator produces its files here instead, which is why it
// needs its own reader rather than the env one — but from the operator's side it
// is the same question, so it is the same button and the same dialog.
function OrchestratorRowOutputsButton({
  orchestrator,
  active,
}: {
  orchestrator: OrchestratorInfo;
  active: boolean;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  return (
    <IconTooltip label={`View and download ${orchestrator.name} outputs`}>
      <Button
        type="button"
        variant="ghost"
        size="icon"
        className={cn(
          'pointer-events-none size-[26px] flex-none cursor-pointer border-0 bg-transparent text-current opacity-0 transition-[opacity,background-color,color] duration-150 hover:bg-[color-mix(in_oklch,currentColor_12%,transparent)] hover:text-current group-hover:pointer-events-auto group-hover:opacity-100 group-focus-within:pointer-events-auto group-focus-within:opacity-100 [&_svg]:size-4',
          active && 'pointer-events-auto opacity-100',
        )}
        aria-label={`Outputs for orchestrator ${orchestrator.name}`}
        onClick={(event) => {
          event.stopPropagation();
          void dispatch(
            openOutputs({
              kind: 'orchestrator',
              orchestratorId: orchestrator.id,
              name: orchestrator.name,
            }),
          );
        }}
      >
        <Download />
      </Button>
    </IconTooltip>
  );
}

function OrchestratorRowDetailsButton({
  orchestrator,
  active,
}: {
  orchestrator: OrchestratorInfo;
  active: boolean;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  return (
    <IconTooltip label={`Orchestrator ${orchestrator.name} settings`}>
      <Button
        type="button"
        variant="ghost"
        size="icon"
        className={cn(
          'pointer-events-none size-[26px] flex-none cursor-pointer border-0 bg-transparent text-current opacity-0 transition-[opacity,background-color,color] duration-150 hover:bg-[color-mix(in_oklch,currentColor_12%,transparent)] hover:text-current group-hover:pointer-events-auto group-hover:opacity-100 group-focus-within:pointer-events-auto group-focus-within:opacity-100 [&_svg]:size-4',
          active && 'pointer-events-auto opacity-100',
        )}
        aria-label={`Edit orchestrator ${orchestrator.name} settings`}
        onClick={(event) => {
          event.stopPropagation();
          dispatch(openOrchestratorDialog(orchestrator));
        }}
      >
        <MoreHorizontal />
      </Button>
    </IconTooltip>
  );
}
