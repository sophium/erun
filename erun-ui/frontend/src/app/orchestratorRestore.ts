import type { OrchestratorInfo } from './slices/orchestratorsSlice';

// What the backend answers when boot asks which orchestrator to reopen: the one
// that was open when the desktop last ran, or the one a restart handed off —
// only the hand-off carries a prompt to auto-run.
export interface OrchestratorReopenTarget {
  orchestratorId?: string;
  resumePrompt?: string;
}

// How boot should reopen an orchestrator: which one, and whether the session is
// handed a task on resume.
export interface OrchestratorRestorePlan {
  id: string;
  resumePrompt: string;
}

// planOrchestratorRestore decides what a launch actually restores, given the
// target and the definitions that exist right now. A target naming an
// orchestrator that has since been deleted restores nothing, and neither does a
// transient (Investigate) session, which has no definition to resume — in both
// cases boot falls through to the default environment selection instead.
export function planOrchestratorRestore(
  target: OrchestratorReopenTarget | null | undefined,
  orchestrators: readonly OrchestratorInfo[],
): OrchestratorRestorePlan | null {
  const id = target?.orchestratorId?.trim() ?? '';
  if (!id) {
    return null;
  }
  if (!orchestrators.some((orchestrator) => orchestrator.id === id && !orchestrator.transient)) {
    return null;
  }
  return { id, resumePrompt: target?.resumePrompt?.trim() ?? '' };
}
