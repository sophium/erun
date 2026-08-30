import type { WhipDefaultTarget, WhipTargetSelection } from '@/app/model';

import type { main } from '../../wailsjs/go/models';

// defaultWhipTargetSelection is what a freshly opened whip popover starts
// from: the sidebar's current focus, checked by itself, nothing else. Never
// "everything" -- an unfocused surface (dashboard, or nothing at all) starts
// from an empty selection rather than defaulting to every configured target
// (erun#1700's "an empty selection is not all", read the other way: no
// default is not a default of all either).
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

// resolveWhipTargetRefs turns the working selection into the explicit list
// WhipNow requires, against the freshest known population: an "all" category
// resolves to every id currently listed (so an orchestrator that started
// after "Select all orchestrators" was clicked, but before Whip actually ran,
// is still included -- erun#1700's "group selects follow the population, not
// a snapshot"); a "custom" category resolves to the checked ids that are
// still present in that population, dropping any that disappeared.
export function resolveWhipTargetRefs(
  selection: WhipTargetSelection,
  targets: main.uiWhipTargetList,
): main.uiWhipTargetRef[] {
  const refs: main.uiWhipTargetRef[] = [];
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

// countWhipTargets is the count the primary action states before it acts
// (erun#1700's "state the count before acting"), resolved against the same
// freshest population resolveWhipTargetRefs uses so the number never lies
// about what a click would actually push.
export function countWhipTargets(
  selection: WhipTargetSelection,
  targets: main.uiWhipTargetList,
): number {
  return resolveWhipTargetRefs(selection, targets).length;
}
