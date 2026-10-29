import type { WhipDefaultTarget, WhipTargetSelection } from '@/app/model';

import type { orchestrator } from '../../wailsjs/go/models';

// Starts from the sidebar's focus only; with no focus, nothing is selected.
export function defaultWhipTargetSelection(defaultTarget: WhipDefaultTarget): WhipTargetSelection {
  if (!defaultTarget) {
    return {
      environmentMode: 'custom',
      orchestratorMode: 'custom',
      selectedEnvironmentIds: [],
      selectedOrchestratorIds: [],
    };
  }
  return {
    environmentMode: 'custom',
    orchestratorMode: 'custom',
    selectedEnvironmentIds: defaultTarget.kind === 'environment' ? [defaultTarget.id] : [],
    selectedOrchestratorIds: defaultTarget.kind === 'orchestrator' ? [defaultTarget.id] : [],
  };
}

export function toggleWhipEnvironment(
  selection: WhipTargetSelection,
  id: string,
  checked: boolean,
): WhipTargetSelection {
  const next = new Set(selection.selectedEnvironmentIds);
  if (checked) {
    next.add(id);
  } else {
    next.delete(id);
  }
  return { ...selection, environmentMode: 'custom', selectedEnvironmentIds: [...next] };
}

export function toggleWhipOrchestrator(
  selection: WhipTargetSelection,
  id: string,
  checked: boolean,
): WhipTargetSelection {
  const next = new Set(selection.selectedOrchestratorIds);
  if (checked) {
    next.add(id);
  } else {
    next.delete(id);
  }
  return { ...selection, orchestratorMode: 'custom', selectedOrchestratorIds: [...next] };
}

export function selectAllWhipEnvironments(selection: WhipTargetSelection): WhipTargetSelection {
  return { ...selection, environmentMode: 'all' };
}

export function selectAllWhipOrchestrators(selection: WhipTargetSelection): WhipTargetSelection {
  return { ...selection, orchestratorMode: 'all' };
}

export function selectAllWhipTargets(selection: WhipTargetSelection): WhipTargetSelection {
  return { ...selection, environmentMode: 'all', orchestratorMode: 'all' };
}

// resolveWhipTargetRefs resolves "all" against the current population, so
// orchestrators started after the click are included, and drops vanished ids.
export function resolveWhipTargetRefs(
  selection: WhipTargetSelection,
  targets: orchestrator.WhipTargetList,
): orchestrator.WhipTargetRef[] {
  const refs: orchestrator.WhipTargetRef[] = [];
  if (selection.environmentMode === 'all') {
    for (const env of targets.environments) {
      refs.push({ kind: 'environment', id: env.id });
    }
  } else {
    const known = new Set(targets.environments.map((env) => env.id));
    for (const id of selection.selectedEnvironmentIds) {
      if (known.has(id)) {
        refs.push({ kind: 'environment', id });
      }
    }
  }
  if (selection.orchestratorMode === 'all') {
    for (const orchestrator of targets.orchestrators) {
      refs.push({ kind: 'orchestrator', id: orchestrator.id });
    }
  } else {
    const known = new Set(targets.orchestrators.map((orchestrator) => orchestrator.id));
    for (const id of selection.selectedOrchestratorIds) {
      if (known.has(id)) {
        refs.push({ kind: 'orchestrator', id });
      }
    }
  }
  return refs;
}

export function countWhipTargets(
  selection: WhipTargetSelection,
  targets: orchestrator.WhipTargetList,
): number {
  return resolveWhipTargetRefs(selection, targets).length;
}
