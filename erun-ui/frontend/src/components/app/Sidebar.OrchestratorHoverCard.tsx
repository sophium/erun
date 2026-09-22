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
  const { open, setOpen, openNow, closeSoon } = useHoverCardOpenState();

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
      {/* w-90 (22.5rem / 360px), not the environment card's w-72: this card's
          value column has to hold a background shell's `Shell running for
          10m45s:` label on one line at the shared 12px type size, and at w-72
          that label wraps to two, which doubles the Doing row's height budget
          on its own. The card is wide because it lists nine environments, not
          because its values are long -- long values still truncate, never
          widen it (OrchestratorEnvironments). The environment card keeps
          w-72. */}
      <PopoverContent
        side="right"
        align="start"
        sideOffset={8}
        onOpenAutoFocus={(event) => {
          event.preventDefault();
        }}
        onMouseEnter={openNow}
        onMouseLeave={closeSoon}
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
        {/* Single zone: unlike EnvHoverCard this card has no stable-identity vs
            live-state split, so it skips spacing level 3 rather than inventing
            a one-zone version of it (Sidebar.HoverCardRow.tsx). tabular-nums
            lives on this container, not per row. */}
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
          {/* wide: an environment's busy detail names a real holder ("held by
              gradle-build"), which needs the card's full content width to read
              without eliding -- the narrow value column every other row shares
              with the "Environments" label leaves too little room for it. */}
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
    // The shell command is a machine-authored string of unbounded length --
    // observed at ~170 characters, which wrapped this row over ten lines and
    // pushed the Environments list, the card's actual subject, out of sight.
    // So the readable half leads in the value treatment and the command drops
    // to a muted, held-length caption (spacing level 1: value plus its own
    // caption, one fact read together). Clamping is presentation only: the
    // command itself is untouched in the read model, still fully in the DOM,
    // and recoverable from `title`.
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
    // Distinct from "Not started": the session is up and simply between turns.
    return <Muted>Idle, waiting for input</Muted>;
  }
  return <span className={HOVER_CARD_VALUE_STACK_CLASS}>{lines}</span>;
}

// Each linked environment renders what it is doing, joined from the
// environment-activity poller rather than collected here (see
// orchestratorEnvironmentActivity.ts) -- this is the fix for the defect that
// motivated the whole card: it used to name two environments and say nothing
// about either. min-w-0 on both the row and its text column is required for
// truncate to engage on a grid/flex child (a long environment name or a long
// busy detail elides instead of blowing out the card's fixed w-90).
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
              {/* Identifier: its tail is recoverable from the row it names, so it
                  clips on one line with the full value in `title` -- the rule
                  HOVER_CARD_TRUNCATE_CLASS owns (Sidebar.HoverCardRow.tsx). */}
              <span className={`${HOVER_CARD_TRUNCATE_CLASS} font-semibold`} title={line.name}>
                {line.name}
              </span>
              {/* Prose, not an identifier: the check-failed line's second clause
                  names the remedy ("open it to check directly"), which is the
                  only thing this row exists to deliver. Prose wraps rather than
                  clips -- the `dd`'s own `break-words`, the deploy overlay's
                  behaviour, and InlineAlert's `[overflow-wrap:anywhere]` -- so a
                  narrow card cannot discard the actionable half of the string
                  while keeping the part the operator cannot act on. */}
              <span className={`block ${HOVER_CARD_CAPTION_CLASS}`}>{line.status}</span>
              {line.roleLabel && (
                <span className={`block truncate ${HOVER_CARD_CAPTION_CLASS}`}>
                  {line.roleLabel}
                </span>
              )}
              {line.usage && (
                // Stale renders degraded, not amber: same reasoning as
                // EnvHoverCard's UsageState -- an unmeasured age is not a fault
                // and must not look more alarming than it is.
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

// Whether erun has been nudging this orchestrator (orchestrator_pacing.go):
// a session that has gone quiet gets restated the pacing contract every 15s
// tick once it is stale, capped after orchestratorPacingMaxNudges. The live
// cap gauge (nudgeCount/nudgeCapped) only means anything while running, but
// the cumulative history survives a stopped session (persisted per
// orchestrator id) -- see orchestratorHasNudgeHistory, which is why this row
// can still render while stopped.
//
// The history and the could-not line are one fact read together, so they stack
// (spacing level 1, HOVER_CARD_VALUE_STACK_CLASS): what erun has done about
// this session, and whether it can do anything about it from here. The second
// is a caption rather than an alert -- nothing the operator did caused it and
// nothing is failing; the session simply belongs to another desktop, which is
// also why it is not the amber treatment restartRequired earns.
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
