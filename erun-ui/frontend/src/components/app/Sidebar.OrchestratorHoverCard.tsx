import { Popover, PopoverAnchor, PopoverContent } from 'erun-kit';
import { TriangleAlert } from 'lucide-react';
import * as React from 'react';

import { formatElapsed } from '@/app/activityQueueState';
import { orchestratorBusyElapsed } from '@/app/orchestratorBusyLabel';
import { orchestratorEnvironmentLine } from '@/app/orchestratorEnvironmentActivity';
import {
  orchestratorHasNudgeHistory,
  orchestratorNudgeSummary,
  orchestratorPacingUnreachableNote,
} from '@/app/orchestratorNudgeSummary';
import type { OrchestratorInfo } from '@/app/slices/orchestratorsSlice';
import { useHoverCardOpenState } from '@/app/useHoverCardOpenState';
import {
  HOVER_CARD_ALERT_CLASS,
  HOVER_CARD_CAPTION_CLASS,
  HOVER_CARD_CAPTION_DEGRADED_CLASS,
  HOVER_CARD_CLAMP_2_CLASS,
  HOVER_CARD_GRID_CLASS,
  HOVER_CARD_TRUNCATE_CLASS,
  HOVER_CARD_VALUE_STACK_CLASS,
  HoverCardBadge,
  HoverCardMuted,
  HoverCardRow,
  HoverCardTitle,
} from '@/components/app/Sidebar.HoverCardRow';
import { StatusDotGlyph } from '@/components/app/Sidebar.StatusDot';

// OrchestratorHoverCard mirrors EnvHoverCard's popover behaviour for orchestrator rows.
export function OrchestratorHoverCard({
  className,
  orchestrator,
  children,
}: {
  className?: string;
  orchestrator: OrchestratorInfo;
  children: React.ReactNode;
}): React.ReactElement {
  const { open, setOpen, hoverEnter, hoverLeave, focusEnter, focusLeave, refuseDismissWhileHeld } =
    useHoverCardOpenState();

  const running = orchestrator.status === 'running';

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverAnchor asChild>
        <div
          className={className}
          onMouseEnter={hoverEnter}
          onMouseLeave={hoverLeave}
          onFocusCapture={focusEnter}
          onBlurCapture={focusLeave}
        >
          {children}
        </div>
      </PopoverAnchor>
      {/* w-90 keeps a background shell's "Shell running for 10m45s:" label on one line. */}
      <PopoverContent
        side="right"
        align="start"
        sideOffset={8}
        onOpenAutoFocus={(event) => {
          event.preventDefault();
        }}
        // Same routing as EnvHoverCard's.
        onFocusOutside={refuseDismissWhileHeld}
        onMouseEnter={hoverEnter}
        onMouseLeave={hoverLeave}
        className="w-90 p-0"
        role="dialog"
        aria-label={`${orchestrator.name} details`}
      >
        <div className="border-b border-border px-3 py-2">
          <div className="flex items-center gap-1.5">
            <HoverCardTitle>{orchestrator.name}</HoverCardTitle>
            {orchestrator.transient && <HoverCardBadge>Transient</HoverCardBadge>}
          </div>
        </div>
        <dl className={`${HOVER_CARD_GRID_CLASS} tabular-nums px-3 py-2.5`}>
          <HoverCardRow label="Status">{running ? 'Running' : 'Stopped'}</HoverCardRow>
          {running && orchestrator.restartRequired && (
            <HoverCardRow label="Restart">
              <span className={`flex items-start gap-1.5 ${HOVER_CARD_ALERT_CLASS}`}>
                <TriangleAlert aria-hidden="true" className="mt-0.5 size-3.5 flex-none" />
                <span>
                  Its environments changed while it was running. It still holds tools for the old
                  set until restarted.
                </span>
              </span>
            </HoverCardRow>
          )}
          <HoverCardRow label="Doing">
            <OrchestratorDoing orchestrator={orchestrator} running={running} />
          </HoverCardRow>
          {/* wide: busy details name a real holder and need the full content width. */}
          <HoverCardRow label="Environments" wide>
            <OrchestratorEnvironments environments={orchestrator.environments} />
          </HoverCardRow>
          {(running ||
            orchestratorHasNudgeHistory(orchestrator) ||
            Boolean(orchestrator.pacingUnreachable)) && (
            <HoverCardRow label="Nudges">
              <OrchestratorNudges orchestrator={orchestrator} />
            </HoverCardRow>
          )}
        </dl>
      </PopoverContent>
    </Popover>
  );
}

// A working turn and a background shell are independent (a shell can outlive
// its turn), so both are named when both are true.
function OrchestratorDoing({
  orchestrator,
  running,
}: {
  orchestrator: OrchestratorInfo;
  running: boolean;
}): React.ReactElement {
  if (!running) {
    return <Muted>Not started</Muted>;
  }
  // The card header already names the orchestrator; the sidebar tooltips carry
  // the name because they hang off a bare icon.
  const now = Date.now();
  const lines: React.ReactNode[] = [];
  if (orchestrator.busy) {
    const elapsed = orchestratorBusyElapsed(orchestrator.busyAtUnix, now);
    lines.push(<span key="busy">{elapsed ? `Working, for ${elapsed}` : 'Working'}</span>);
  }
  if (orchestrator.shellRunning) {
    const shellElapsed = orchestrator.shellStartedAtUnix
      ? formatElapsed(new Date(orchestrator.shellStartedAtUnix * 1000).toISOString(), now).trim()
      : '';
    const shell = shellElapsed ? `Shell running for ${shellElapsed}` : 'Shell running';
    // The command is unbounded machine output, so it drops to a clamped caption;
    // the full value stays in the DOM and in `title`.
    lines.push(
      orchestrator.shellCommand ? (
        <span key="shell" className={HOVER_CARD_VALUE_STACK_CLASS}>
          <span>{shell}:</span>
          <span
            className={`${HOVER_CARD_CLAMP_2_CLASS} ${HOVER_CARD_CAPTION_CLASS}`}
            title={orchestrator.shellCommand}
          >
            {orchestrator.shellCommand}
          </span>
        </span>
      ) : (
        <span key="shell">{shell}</span>
      ),
    );
  }
  if (lines.length === 0) {
    return <Muted>Idle, waiting for input</Muted>;
  }
  return <span className={HOVER_CARD_VALUE_STACK_CLASS}>{lines}</span>;
}

// min-w-0 on the row and its text column lets truncate engage on a grid/flex child.
function OrchestratorEnvironments({
  environments,
}: {
  environments: OrchestratorInfo['environments'];
}): React.ReactElement {
  if (environments.length === 0) {
    return <Muted>None linked</Muted>;
  }
  return (
    <span className="grid gap-1.5">
      {environments.map((env) => {
        const line = orchestratorEnvironmentLine(env);
        return (
          <span key={line.key} className="flex min-w-0 items-start gap-1.5">
            <span
              className="mt-0.5 flex w-2.5 flex-none items-center justify-center"
              aria-hidden="true"
            >
              {line.dot && <StatusDotGlyph state={line.dot} />}
            </span>
            <span className="min-w-0 flex-1">
              <span className={`${HOVER_CARD_TRUNCATE_CLASS} font-semibold`} title={line.name}>
                {line.name}
              </span>
              {/* Prose wraps rather than clips: the second clause carries the remedy. */}
              <span className={`block ${HOVER_CARD_CAPTION_CLASS}`}>{line.status}</span>
              {line.roleLabel && (
                <span className={`block truncate ${HOVER_CARD_CAPTION_CLASS}`}>
                  {line.roleLabel}
                </span>
              )}
              {line.usage && (
                // Stale renders degraded, not amber: an unmeasured age is not a fault.
                <span
                  className={`block truncate ${
                    line.usageStale ? HOVER_CARD_CAPTION_DEGRADED_CLASS : HOVER_CARD_CAPTION_CLASS
                  }`}
                >
                  {line.usage}
                  {line.usageStale ? ' (stale)' : ''}
                </span>
              )}
            </span>
          </span>
        );
      })}
    </span>
  );
}

// Pacing-nudge history survives a stopped session, so this row can render
// while stopped. The could-not line is a caption, not an alert: the session
// belongs to another desktop and nothing is failing.
function OrchestratorNudges({
  orchestrator,
}: {
  orchestrator: OrchestratorInfo;
}): React.ReactElement {
  const summary = orchestratorNudgeSummary(orchestrator, Date.now());
  const unreachableNote = orchestratorPacingUnreachableNote(orchestrator);
  if (orchestrator.nudgeCapped) {
    return (
      <span className={`flex items-start gap-1.5 ${HOVER_CARD_ALERT_CLASS}`}>
        <TriangleAlert aria-hidden="true" className="mt-0.5 size-3.5 flex-none" />
        <span>{summary}</span>
      </span>
    );
  }
  const hasHistory = orchestrator.autoNudgeCount > 0 || orchestrator.whipCount > 0;
  if (!unreachableNote) {
    return hasHistory ? <span>{summary}</span> : <Muted>{summary}</Muted>;
  }
  return (
    <span className={HOVER_CARD_VALUE_STACK_CLASS}>
      {hasHistory ? <span>{summary}</span> : <Muted>{summary}</Muted>}
      <span className={HOVER_CARD_CAPTION_CLASS}>{unreachableNote}</span>
    </span>
  );
}

function Muted({ children }: { children: React.ReactNode }): React.ReactElement {
  return <HoverCardMuted>{children}</HoverCardMuted>;
}
