import type { OrchestratorInfo } from './slices/orchestratorsSlice';

// A reopen reattaches to the orchestrator's background agent, so an id suffices.
export interface OrchestratorReopenRef {
  orchestratorId: string;
}

// orchestratorId owns the single terminal pane; alsoReopen lists the rest.
export interface OrchestratorReopenTarget {
  orchestratorId?: string;
  alsoReopen?: OrchestratorReopenRef[];
}

export interface OrchestratorRestoreOutcome {
  primary: string | null;
  alsoReopen: string[];
}

const trimmed = (value: string | undefined): string => value?.trim() ?? '';

// planOrchestratorRestore drops deleted and transient orchestrators; if the pane
// owner is gone, the most recently opened survivor takes the pane.
export function planOrchestratorRestore(
  target: OrchestratorReopenTarget | null | undefined,
  orchestrators: readonly OrchestratorInfo[],
): OrchestratorRestoreOutcome {
  const source: OrchestratorReopenTarget = target ?? {};
  const exists = (id: string): boolean =>
    orchestrators.some((orchestrator) => orchestrator.id === id && !orchestrator.transient);

  const primaryID = trimmed(source.orchestratorId);
  const seen = new Set<string>();
  const survivingAlso = (source.alsoReopen ?? []).reduce<string[]>((kept, raw) => {
    const id = trimmed(raw.orchestratorId);
    if (id === '' || id === primaryID || seen.has(id) || !exists(id)) {
      return kept;
    }
    seen.add(id);
    kept.push(id);
    return kept;
  }, []);

  if (primaryID && exists(primaryID)) {
    return { primary: primaryID, alsoReopen: survivingAlso };
  }
  const rest = [...survivingAlso];
  const promoted = rest.pop();
  if (promoted === undefined) {
    return { primary: null, alsoReopen: [] };
  }
  return { primary: promoted, alsoReopen: rest };
}
