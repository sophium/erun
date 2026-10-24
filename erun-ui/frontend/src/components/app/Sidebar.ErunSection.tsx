import { Button, cn, EmptyState, IconTooltip } from 'erun-kit';
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
import { orchestratorBusyLabel } from '@/app/orchestratorBusyLabel';
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
import { InlineAlert } from '@/components/app/InlineAlert';
import { BusyRowSpinner } from '@/components/app/Sidebar.BusyRowSpinner';
import { OrchestratorHoverCard } from '@/components/app/Sidebar.OrchestratorHoverCard';
import { StatusDotGlyph } from '@/components/app/Sidebar.StatusDot';

// ErunSection is the host-side control plane: AI orchestrators by tenant, plus erun-global actions.
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
  const activeOrchestratorId = useAppSelector((state) => {
    const activeSessionId = state.terminal.sessionId;
    if (activeSessionId <= 0) {
      return '';
    }
    return state.orchestrators.items.find((o) => o.sessionId === activeSessionId)?.id ?? '';
  });
  // Doctor needs a target, so disable without a selection.
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

  // boot() restores the post-restart orchestrator; this only loads the list.
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
        // Inset to the sidebar's 3.5 gutter, which the primitive does not carry.
        <div className="px-3.5 pb-1">
          <InlineAlert>{error}</InlineAlert>
        </div>
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
  // Scoped to this orchestrator's session.
  const busy = useAppSelector(
    (state) =>
      orchestrator.sessionId > 0 &&
      state.aiActivity.aiBusyBySession[orchestrator.sessionId] === true,
  );
  // A background shell can outlive its turn; shown only when the turn is not spinning.
  const shellActivity = useAppSelector((state) =>
    orchestrator.sessionId > 0
      ? state.orchestratorShellActivity.bySession[orchestrator.sessionId]
      : undefined,
  );
  return (
    <li>
      {/* Row styling moves onto the hover card's anchor so the <li> keeps list semantics. */}
      <OrchestratorHoverCard
        className={cn(
          'group mr-1 ml-1 flex h-8 items-center gap-1.5 rounded-md pr-1.5 pl-3.5 text-foreground hover:bg-sidebar-accent hover:text-sidebar-accent-foreground',
          active &&
            'bg-primary text-primary-foreground shadow-sm hover:bg-primary hover:text-primary-foreground',
        )}
        orchestrator={orchestrator}
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
        {busy && (
          <OrchestratorBusyIndicator
            name={orchestrator.name}
            busyAtUnix={orchestrator.busyAtUnix}
          />
        )}
        {!busy && shellActivity?.running && (
          <OrchestratorShellIndicator name={orchestrator.name} activity={shellActivity} />
        )}
        <OrchestratorRowActions orchestrator={orchestrator} running={running} active={active} />
      </OrchestratorHoverCard>
    </li>
  );
}

// OrchestratorBusyIndicator explains itself on hover like other sidebar spinners.
function OrchestratorBusyIndicator({
  name,
  busyAtUnix,
}: {
  name: string;
  busyAtUnix: number | undefined;
}): React.ReactElement {
  const label = orchestratorBusyLabel(name, busyAtUnix, Date.now());
  return (
    <IconTooltip label={label}>
      <BusyRowSpinner label={label} />
    </IconTooltip>
  );
}

// OrchestratorShellIndicator names the running command and elapsed time.
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

// OrchestratorRowActions: a status dot plus "…" for the dialog; transient orchestrators get only the dot.
function OrchestratorRowActions({
  orchestrator,
  running,
  active,
}: {
  orchestrator: OrchestratorInfo;
  running: boolean;
  active: boolean;
}): React.ReactElement {
  // A live session's toolset is fixed at launch, so a re-scope needs a restart; reuses the 'failed' shape.
  const restartRequired = running && orchestrator.restartRequired;
  return (
    <>
      <span
        className="flex size-[18px] flex-none items-center justify-center"
        role="img"
        aria-label={
          restartRequired
            ? `Orchestrator ${orchestrator.name} is running but needs a restart to apply its new environments`
            : running
              ? `Orchestrator ${orchestrator.name} is running`
              : `Orchestrator ${orchestrator.name} is stopped`
        }
      >
        <StatusDotGlyph state={restartRequired ? 'failed' : running ? 'running' : 'stopped'} />
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

// Orchestrator outputs are produced on this host, so they need their own reader.
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
