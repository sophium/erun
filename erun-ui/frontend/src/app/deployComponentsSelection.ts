import type { UIDeployableComponent } from '@/types';

// normalizeDeployComponents coerces the read-model result into a stable array
// (the Wails binding can hand back null for an empty slice).
export function normalizeDeployComponents(
  raw: UIDeployableComponent[] | null | undefined,
): UIDeployableComponent[] {
  return (raw ?? []).map((component) => ({
    name: component.name,
    runtime: component.runtime,
    source: component.source,
    selected: component.selected,
  }));
}

// deployComponentDefaultNames returns the names the read model marks selected —
// the env's current resolved default selection, used to seed the checklist and
// to detect whether the operator has changed it.
export function deployComponentDefaultNames(options: UIDeployableComponent[]): string[] {
  return options.filter((option) => option.selected).map((option) => option.name);
}

// toggleDeployComponentName adds or removes a name from the working selection,
// preserving the checklist's display order (options order) and de-duplicating.
export function toggleDeployComponentName(
  options: UIDeployableComponent[],
  selection: string[],
  name: string,
  checked: boolean,
): string[] {
  const next = new Set(selection);
  if (checked) {
    next.add(name);
  } else {
    next.delete(name);
  }
  return options.filter((option) => next.has(option.name)).map((option) => option.name);
}

// deployComponentSelectionChanged reports whether the working selection differs
// from the read model's default (the saved baseline), so the Runtime tab can
// enable "Save as default" only when there is a real change to persist.
export function deployComponentSelectionChanged(
  options: UIDeployableComponent[],
  selection: string[],
): boolean {
  const baseline = deployComponentDefaultNames(options);
  if (baseline.length !== selection.length) {
    return true;
  }
  const selected = new Set(selection);
  return baseline.some((name) => !selected.has(name));
}

// deployComponentLabel renders a user-facing label: the runtime item names its
// role and source (the operator does not manage the raw chart), while component
// charts show their chart name directly.
export function deployComponentLabel(component: UIDeployableComponent): string {
  if (component.runtime) {
    const published = component.source === 'published-chart' ? ' (published)' : '';
    return `Runtime — ${component.name}${published}`;
  }
  return component.name;
}
