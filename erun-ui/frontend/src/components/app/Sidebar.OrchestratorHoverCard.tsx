import { Popover, PopoverAnchor, PopoverContent } from 'erun-kit';
import { TriangleAlert } from 'lucide-react';
import * as React from 'react';

import { formatElapsed } from '@/app/activityQueueState';
import { orchestratorBusyElapsed } from '@/app/orchestratorBusyLabel';
import { orchestratorEnvironmentLine } from '@/app/orchestratorEnvironmentActivity';
import { orchestratorNudgeSummary } from '@/app/orchestratorNudgeSummary';
import type { OrchestratorInfo } from '@/app/slices/orchestratorsSlice';
import {
  HOVER_CARD_CAPTION_CLASS,
  HoverCardBadge,
  HoverCardMuted,
  HoverCardRow,
  HoverCardTitle,
} from '@/components/app/Sidebar.HoverCardRow';
import { StatusDotGlyph } from '@/components/app/Sidebar.StatusDot';

// OrchestratorHoverCard gives an orchestrator row the same hover treatment the
// environment row has had since EnvHoverCard: a Popover, not a tooltip, because
// a multi-field card does not belong in a tooltip (erun-ui/AGENTS.md).
//
// Before this, an orchestrator row explained nothing on hover. The only hover
// surface in the section was an IconTooltip on the spinner icon, which exists
// only WHILE the orchestrator is spinning -- so an idle orchestrator, the common
// case, had no hover target at all, and a working one offered a tooltip on a
// 12px icon rather than on the row the operator is pointing at (#1343).
//
// Deliberately mirrors EnvHoverCard rather than inventing a second hover
// pattern: same Popover, same open-now / close-soon grace so moving the pointer
// onto the card does not dismiss it, same non-focus-trapping semantics so the
// row's own click-to-open still works, same width and type scale. Consistency
// is the point (Nielsen #4) -- the two sidebar row kinds should not behave
// differently for the same gesture.
export function OrchestratorHoverCard({
  className,
  orchestrator,
  children,
}: {
  className?: string;
  orchestrator: OrchestratorInfo;
  children: React.ReactNode;
}): React.ReactElement {
  const [open, setOpen] = React.useState(false);
  const closeTimer = React.useRef(0);
  const openNow = React.useCallback(() => {
    window.clearTimeout(closeTimer.current);
    setOpen(true);
  }, []);
  const closeSoon = React.useCallback(() => {
    window.clearTimeout(closeTimer.current);
    // Small grace so moving the pointer from the row onto the card doesn't close it.
    closeTimer.current = window.setTimeout(() => {
      setOpen(false);
    }, 120);
  }, []);
  React.useEffect(
    () => () => {
      window.clearTimeout(closeTimer.current);
    },
    [],
  );

  const running = orchestrator.status === 'running';

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverAnchor asChild>
        <div
          className={className}
          onMouseEnter={openNow}
          onMouseLeave={closeSoon}
          onFocusCapture={openNow}
          onBlurCapture={closeSoon}
        >
          {children}
        </div>
      </PopoverAnchor>
      <PopoverContent
        side="right"
        align="start"
        sideOffset={8}
        onOpenAutoFocus={(event) => {
          event.preventDefault();
        }}
        onMouseEnter={openNow}
        onMouseLeave={closeSoon}
        className="w-72 p-0 text-sm"
        role="dialog"
        aria-label={`${orchestrator.name} details`}
      >
        <div className="border-b border-border px-3 py-2">
          <div className="flex items-center gap-1.5">
            <HoverCardTitle>{orchestrator.name}</HoverCardTitle>
            {orchestrator.transient && <HoverCardBadge>Transient</HoverCardBadge>}
          </div>
        </div>
        <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1.5 px-3 py-2.5">
          <HoverCardRow label="Status">{running ? 'Running' : 'Stopped'}</HoverCardRow>
          {running && orchestrator.restartRequired && (
            <HoverCardRow label="Restart">
              <span className="flex items-start gap-1.5 text-amber-600">
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
          {/* wide: an environment's busy detail names a real holder ("held by
              gradle-build"), which needs the card's full content width to read
              without eliding -- the narrow value column every other row shares
              with the "Environments" label leaves too little room for it. */}
          <HoverCardRow label="Environments" wide>
            <OrchestratorEnvironments environments={orchestrator.environments} />
          </HoverCardRow>
          {running && (
            <HoverCardRow label="Nudges">
              <OrchestratorNudges orchestrator={orchestrator} />
            </HoverCardRow>
          )}
        </dl>
      </PopoverContent>
    </Popover>
  );
}

// The whole reason the row is worth hovering: what is this orchestrator doing
// right now, and for how long. A working turn and a background shell are
// independent facts -- a shell can outlive the turn that started it -- so both
// are named when both are true rather than one hiding the other.
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
  // The card's own header already names the orchestrator, so these lines must
  // not repeat it. The sidebar's tooltip labels (orchestratorBusyLabel,
  // orchestratorShellLabel) deliberately DO carry the name, because they hang
  // off a bare icon with no other context -- same facts, different surface.
  const now = Date.now();
  const lines: string[] = [];
  if (orchestrator.busy) {
    const elapsed = orchestratorBusyElapsed(orchestrator.busyAtUnix, now);
    lines.push(elapsed ? `Working, for ${elapsed}` : 'Working');
  }
  if (orchestrator.shellRunning) {
    const shellElapsed = orchestrator.shellStartedAtUnix
      ? formatElapsed(new Date(orchestrator.shellStartedAtUnix * 1000).toISOString(), now).trim()
      : '';
    const shell = shellElapsed ? `Shell running for ${shellElapsed}` : 'Shell running';
    lines.push(orchestrator.shellCommand ? `${shell}: ${orchestrator.shellCommand}` : shell);
  }
  if (lines.length === 0) {
    // Distinct from "Not started": the session is up and simply between turns.
    return <Muted>Idle, waiting for input</Muted>;
  }
  return (
    <span className="grid gap-0.5">
      {lines.map((line) => (
        <span key={line}>{line}</span>
      ))}
    </span>
  );
}

// Each linked environment renders what it is doing, joined from the
// environment-activity poller rather than collected here (see
// orchestratorEnvironmentActivity.ts) -- this is the fix for the defect that
// motivated the whole card: it used to name two environments and say nothing
// about either. min-w-0 on both the row and its text column is required for
// truncate to engage on a grid/flex child (a long environment name or a long
// busy detail elides instead of blowing out the card's fixed w-72).
function OrchestratorEnvironments({
  environments,
}: {
  environments: OrchestratorInfo['environments'];
}): React.ReactElement {
  if (environments.length === 0) {
    // An orchestrator with no links is a real state, not a failure to load --
    // and it is worth naming, because it is why its tools are empty.
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
              <span className="block truncate font-medium">{line.name}</span>
              <span className={`block truncate ${HOVER_CARD_CAPTION_CLASS}`}>{line.status}</span>
              {line.usage && (
                <span
                  className={
                    line.usageStale
                      ? 'block truncate text-xs tabular-nums text-amber-700 dark:text-amber-400'
                      : `block truncate tabular-nums ${HOVER_CARD_CAPTION_CLASS}`
                  }
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

// Whether erun has been nudging this orchestrator (orchestrator_pacing.go):
// a session that has gone quiet gets restated the pacing contract every 15s
// tick once it is stale, capped after orchestratorPacingMaxNudges. Rendered
// only while running -- a stopped orchestrator's pacing state does not
// survive past its session, so there is nothing to report.
function OrchestratorNudges({
  orchestrator,
}: {
  orchestrator: OrchestratorInfo;
}): React.ReactElement {
  const summary = orchestratorNudgeSummary(orchestrator, Date.now());
  if (orchestrator.nudgeCapped) {
    return (
      <span className="flex items-start gap-1.5 text-amber-600">
        <TriangleAlert aria-hidden="true" className="mt-0.5 size-3.5 flex-none" />
        <span>{summary}</span>
      </span>
    );
  }
  if (orchestrator.autoNudgeCount > 0 || orchestrator.whipCount > 0) {
    return <span>{summary}</span>;
  }
  return <Muted>{summary}</Muted>;
}

function Muted({ children }: { children: React.ReactNode }): React.ReactElement {
  return <HoverCardMuted>{children}</HoverCardMuted>;
}
