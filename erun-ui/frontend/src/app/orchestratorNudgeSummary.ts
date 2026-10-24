import { orchestratorBusyElapsed } from '@/app/orchestratorBusyLabel';
import type { OrchestratorInfo } from '@/app/slices/orchestratorsSlice';

type NudgeSummaryFields = Pick<
  OrchestratorInfo,
  | 'nudgeCount'
  | 'nudgeCapped'
  | 'autoNudgeCount'
  | 'lastAutoNudgeAtUnix'
  | 'whipCount'
  | 'lastWhipAtUnix'
  | 'lastCappedAtUnix'
  | 'nudgeHistoryUnreadable'
>;

// orchestratorPacingUnreachableNote flags a session this desktop holds no PTY of,
// so it cannot pace it; '' when the desktop owns the session.
export function orchestratorPacingUnreachableNote(orchestrator: {
  pacingUnreachable?: boolean;
}): string {
  return orchestrator.pacingUnreachable
    ? 'Not paced from this desktop — erun has no session for it here.'
    : '';
}

// orchestratorNudgeSummary reports cumulative history, since nudgeCount resets on
// each reply; nudgeCapped is read live. Unreadable history is never "Not nudged".
export function orchestratorNudgeSummary(orchestrator: NudgeSummaryFields, nowMs: number): string {
  if (orchestrator.nudgeCapped) {
    return `Stopped nudging after ${String(orchestrator.nudgeCount)} attempts — reply or restart`;
  }
  const facts: string[] = [];
  if (orchestrator.autoNudgeCount > 0) {
    facts.push(
      historyFact('Nudged', orchestrator.autoNudgeCount, orchestrator.lastAutoNudgeAtUnix, nowMs),
    );
  }
  if (orchestrator.whipCount > 0) {
    facts.push(historyFact('Whipped', orchestrator.whipCount, orchestrator.lastWhipAtUnix, nowMs));
  }
  if (orchestrator.lastCappedAtUnix) {
    const elapsed = orchestratorBusyElapsed(orchestrator.lastCappedAtUnix, nowMs);
    facts.push(elapsed ? `previously capped ${elapsed} ago` : 'previously capped');
  }
  if (facts.length === 0 && orchestrator.nudgeHistoryUnreadable) {
    return 'Nudge history unavailable';
  }
  return facts.length > 0 ? facts.join('; ') : 'Not nudged';
}

// orchestratorHasNudgeHistory lets the Nudges row render while stopped.
export function orchestratorHasNudgeHistory(orchestrator: NudgeSummaryFields): boolean {
  return (
    orchestrator.nudgeCapped ||
    orchestrator.autoNudgeCount > 0 ||
    orchestrator.whipCount > 0 ||
    Boolean(orchestrator.lastCappedAtUnix) ||
    Boolean(orchestrator.nudgeHistoryUnreadable)
  );
}

function historyFact(
  verb: string,
  count: number,
  lastAtUnix: number | undefined,
  nowMs: number,
): string {
  const elapsed = orchestratorBusyElapsed(lastAtUnix, nowMs);
  return elapsed ? `${verb} ${String(count)}x, last ${elapsed} ago` : `${verb} ${String(count)}x`;
}
