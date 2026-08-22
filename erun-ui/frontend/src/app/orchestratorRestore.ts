import type { OrchestratorInfo } from './slices/orchestratorsSlice';

// One orchestrator in alsoReopen: its id and the conversation it resumes. The
// backend resolves conversationId the same way for every entry, owner
// included — the exact conversation last known to be running for that
// orchestrator, never a re-derivation from its id, which can land on a
// different, older conversation. Empty only when the backend found
// nothing safe to resume, in which case the orchestrator starts fresh.
export interface OrchestratorReopenRef {
  orchestratorId: string;
  conversationId?: string;
}

// What the backend answers when boot asks which orchestrators to reopen:
// orchestratorId is the one that OWNS THE TERMINAL PANE — the pane is single,
// so exactly one orchestrator gets it — and alsoReopen lists every other
// orchestrator that was open too, restored alongside it but idle. Only the
// pane owner can carry a resume PROMPT; that is why resumePrompt lives on the
// target itself rather than per id in alsoReopen. A notice means a hand-off
// was refused: the pane owner still reopens, idle, and the notice says why
// nothing is being continued.
export interface OrchestratorReopenTarget {
  orchestratorId?: string;
  conversationId?: string;
  resumePrompt?: string;
  alsoReopen?: OrchestratorReopenRef[];
  notice?: string;
}

// How boot should reopen one orchestrator: which one, which of its
// conversations, and whether that conversation is handed a task on resume.
export interface OrchestratorRestorePlan {
  id: string;
  conversationId: string;
  resumePrompt: string;
}

// OrchestratorRestoreOutcome is what a launch actually restores: the
// orchestrator that ends up owning the pane (or null if there is nothing valid
// to reopen), and every other orchestrator that comes back idle beside it,
// each with the conversation it resumes.
export interface OrchestratorRestoreOutcome {
  primary: OrchestratorRestorePlan | null;
  alsoReopen: OrchestratorReopenRef[];
}

const trimmed = (value: string | undefined): string => value?.trim() ?? '';

// readRestoreNotice is the refusal the backend attached to the target, if any.
// It reads tolerantly because the target crosses a process boundary: a payload
// that omits the field must degrade to "no notice" rather than throw and take
// the whole restore down with it.
export function readRestoreNotice(target: OrchestratorReopenTarget | null | undefined): string {
  return trimmed(target?.notice);
}

// planOrchestratorRestore decides what a launch actually restores, given the
// target and the definitions that exist right now. A candidate naming an
// orchestrator that has since been deleted restores nothing for that
// candidate, and neither does a transient (Investigate) session, which has no
// definition to resume — both are dropped rather than starting a session for
// an id that cannot come back.
//
// If the backend's chosen pane owner cannot be honored this way (deleted
// between shutdown and this launch), the next most-recently-opened surviving
// orchestrator takes the pane instead — the same recency rule the backend used
// to pick a pane owner, just re-applied to the ones still around — rather than
// silently dropping to the default environment selection while other
// orchestrators still start up in the background. Only when nothing in the
// whole set survives does boot fall through to that default.
export function planOrchestratorRestore(
  target: OrchestratorReopenTarget | null | undefined,
  orchestrators: readonly OrchestratorInfo[],
): OrchestratorRestoreOutcome {
  const source: OrchestratorReopenTarget = target ?? {};
  const exists = (id: string): boolean =>
    orchestrators.some((orchestrator) => orchestrator.id === id && !orchestrator.transient);

  const primaryID = trimmed(source.orchestratorId);
  const seen = new Set<string>();
  const survivingAlso = (source.alsoReopen ?? []).reduce<OrchestratorReopenRef[]>((kept, raw) => {
    const id = trimmed(raw.orchestratorId);
    if (id === '' || id === primaryID || seen.has(id) || !exists(id)) {
      return kept;
    }
    seen.add(id);
    kept.push({ orchestratorId: id, conversationId: trimmed(raw.conversationId) });
    return kept;
  }, []);

  if (primaryID && exists(primaryID)) {
    return {
      primary: {
        id: primaryID,
        conversationId: trimmed(source.conversationId),
        resumePrompt: trimmed(source.resumePrompt),
      },
      alsoReopen: survivingAlso,
    };
  }
  const rest = [...survivingAlso];
  const promoted = rest.pop();
  if (promoted === undefined) {
    return { primary: null, alsoReopen: [] };
  }
  return {
    // The promoted survivor carries the conversation the backend already
    // resolved for it as an alsoReopen entry — it must not be dropped just
    // because this orchestrator is now taking the pane instead.
    primary: {
      id: promoted.orchestratorId,
      conversationId: promoted.conversationId ?? '',
      resumePrompt: '',
    },
    alsoReopen: rest,
  };
}
