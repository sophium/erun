import { orchestratorBusyElapsed } from '@/app/orchestratorBusyLabel';
import type { OrchestratorInfo } from '@/app/slices/orchestratorsSlice';

// orchestratorNudgeSummary names the pacing state orchestrator_pacing.go
// already tracks per session: whether erun has had to restate the pacing
// contract into a quiet pane, and whether it gave up after the cap. Capped and
// never-nudged both read as "quiet" without this -- and a capped session is
// the one an operator most needs to notice, since erun has stopped acting on
// its behalf.
export function orchestratorNudgeSummary(
  orchestrator: Pick<OrchestratorInfo, 'nudgeCount' | 'nudgeCapped' | 'lastNudgeAtUnix'>,
  nowMs: number,
): string {
  if (orchestrator.nudgeCapped) {
    return `Stopped nudging after ${String(orchestrator.nudgeCount)} attempts — reply or restart`;
  }
  if (orchestrator.nudgeCount > 0) {
    const count = String(orchestrator.nudgeCount);
    const elapsed = orchestratorBusyElapsed(orchestrator.lastNudgeAtUnix, nowMs);
    return elapsed ? `Nudged ${count}x, last ${elapsed} ago` : `Nudged ${count}x`;
  }
  return 'Not nudged';
}
