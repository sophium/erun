import type { UIDeployableComponent } from '@/types';

// normalizeDeployComponents defends against the Wails binding handing back null
// for an empty slice.
export function normalizeDeployComponents(
  raw: UIDeployableComponent[] | null | undefined,
): UIDeployableComponent[] {
  return (raw ?? []).map((component) => ({
    name: component.name,
    runtime: component.runtime,
    source: component.source,
    selected: component.selected,
    publishedChart: component.publishedChart,
  }));
}

// deployComponentDefaultNames returns the env's current default selection — the
// names the read model marks selected.
export function deployComponentDefaultNames(options: UIDeployableComponent[]): string[] {
  return options.filter((option) => option.selected).map((option) => option.name);
}

// toggleDeployComponentName adds or removes name from the working selection.
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
// from the saved default, so the Runtime tab enables "Save as default" only for
// a real change.
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

// The one runtime chart every published-chart env installs, regardless of
// tenant. It is NOT the release name — that is <tenant>-devops. Mirrors the
// backend constant; keep both in sync.
const PUBLISHED_RUNTIME_CHART_NAME = 'erun-devops';

// deployComponentLabel builds the user-facing label for a deploy row.
//
// For a published runtime, publishedChart is the chart the deploy actually
// installs: the tenant's own <tenant>-devops chart when it is published at the
// chosen version (name it directly), else the canonical erun-devops fallback
// (name it, noting the <tenant>-devops release name it installs under). The erun
// tenant's chart IS erun-devops. A local chart's name is a real repo-local chart
// directory, shown as-is.
export function deployComponentLabel(component: UIDeployableComponent): string {
  if (component.runtime) {
    if (component.source === 'published-chart') {
      const chart = component.publishedChart?.trim() ?? PUBLISHED_RUNTIME_CHART_NAME;
      // Falling back to the canonical chart under a different release name: the
      // env's own <tenant>-devops chart is not published at this version.
      if (
        chart === PUBLISHED_RUNTIME_CHART_NAME &&
        component.name !== PUBLISHED_RUNTIME_CHART_NAME
      ) {
        return `Runtime — published ${PUBLISHED_RUNTIME_CHART_NAME} chart (released as ${component.name})`;
      }
      return `Runtime — published ${chart} chart`;
    }
    return `Runtime — ${component.name}`;
  }
  return component.name;
}
