import type { OrchestratorInfo } from './slices/orchestratorsSlice';

// What the backend answers when boot asks which orchestrator to reopen: the one
// that was open when the desktop last ran, or the one a restart handed off —
// only the hand-off names a conversation and carries a prompt to auto-run. A
// notice means the hand-off was refused: the orchestrator still reopens, idle,
// and the notice says why nothing is being continued.
export interface OrchestratorReopenTarget {
  orchestratorId?: string;
  conversationId?: string;
  resumePrompt?: string;
  notice?: string;
}

// How boot should reopen an orchestrator: which one, which of its conversations,
// and whether that conversation is handed a task on resume.
export interface OrchestratorRestorePlan {
  id: string;
  conversationId: string;
  resumePrompt: string;
}

// planOrchestratorRestore decides what a launch actually restores, given the
// target and the definitions that exist right now. A target naming an
// orchestrator that has since been deleted restores nothing, and neither does a
// transient (Investigate) session, which has no definition to resume — in both
// cases boot falls through to the default environment selection instead.
const trimmed = (value: string | undefined): string => value?.trim() ?? '';

// readRestoreNotice is the refusal the backend attached to the target, if any.
// It reads tolerantly because the target crosses a process boundary: a payload
// that omits the field must degrade to "no notice" rather than throw and take
// the whole restore down with it.
export function readRestoreNotice(target: OrchestratorReopenTarget | null | undefined): string {
  return trimmed(target?.notice);
}

export function planOrchestratorRestore(
  target: OrchestratorReopenTarget | null | undefined,
  orchestrators: readonly OrchestratorInfo[],
): OrchestratorRestorePlan | null {
  const source: OrchestratorReopenTarget = target ?? {};
  const id = trimmed(source.orchestratorId);
  if (!id) {
    return null;
  }
  if (!orchestrators.some((orchestrator) => orchestrator.id === id && !orchestrator.transient)) {
    return null;
  }
  return {
    id,
    conversationId: trimmed(source.conversationId),
    resumePrompt: trimmed(source.resumePrompt),
  };
}
