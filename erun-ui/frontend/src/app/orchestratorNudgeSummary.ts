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

// orchestratorNudgeSummary names the pacing state orchestrator_pacing.go
// tracks per session. nudgeCount/nudgeCapped are the cap's own live budget --
// they reset every time the session answers, so reading them directly once
// collapsed "nudged repeatedly, answering every time" onto the same "Not
// nudged" text as "never needed a nudge" the moment the budget cleared. The
// cumulative fields (autoNudgeCount/whipCount and their last-at timestamps,
// plus lastCappedAtUnix) never reset, so they are what this reports as
// history; nudgeCapped alone is read live, since "currently at the cap" is
// exactly the one fact that must still reflect a rearm.
//
// nudgeHistoryUnreadable is checked before any of that: it means the
// persisted record behind the cumulative fields exists but could not be
// read back, so a zero there is an unverified gap, not a confirmed "never
// nudged" -- a known-unknown must not render as a confident value.
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

function historyFact(
  verb: string,
  count: number,
  lastAtUnix: number | undefined,
  nowMs: number,
): string {
  const elapsed = orchestratorBusyElapsed(lastAtUnix, nowMs);
  return elapsed ? `${verb} ${String(count)}x, last ${elapsed} ago` : `${verb} ${String(count)}x`;
}
