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

// orchestratorPacingUnreachableNote is the could-not half of the Nudges row,
// and the reason this module is not only a summary: a session this desktop did
// not launch -- one started in a terminal, or left behind by a previous desktop
// instance -- is read and displayed like any other, but the pacer decides only
// for sessions the desktop holds a PTY of, so its count simply never moves.
// Nothing in the number itself separates that from an orchestrator erun has
// just checked and found nothing to do about, which is the whole of the defect
// this line answers.
//
// The scope wording is not decoration: this desktop can only speak for itself.
// The session may be perfectly healthy and paced by something else, so the line
// claims exactly one thing -- that no nudge can come from here.
//
// It returns '' whenever this desktop is the one that owns the session, which
// is every ordinary case (including a stopped orchestrator nothing is reporting
// for): "erun cannot pace this from here" is only worth saying when there is
// something to pace.
export function orchestratorPacingUnreachableNote(orchestrator: {
  pacingUnreachable?: boolean;
}): string {
  return orchestrator.pacingUnreachable
    ? 'Not paced from this desktop — erun has no session for it here.'
    : '';
}

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

// orchestratorHasNudgeHistory is true exactly when orchestratorNudgeSummary
// would report something other than "Not nudged": cumulative history that
// survives a stopped session (persisted per orchestrator id, restored on the
// next Start/Restart), or an unreadable record whose absence of history is
// unverified rather than confirmed. The hover card's Nudges row uses this to
// decide whether to render at all while the orchestrator is not running --
// otherwise a stopped orchestrator's real nudge history would be computed by
// the backend and then thrown away by a row that only ever rendered while
// running.
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
