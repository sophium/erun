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

// PUBLISHED_RUNTIME_CHART_NAME is the canonical runtime chart every
// published-chart environment installs, regardless of tenant: the deploy path
// hardcodes oci://<registry>/charts/erun-devops (erun-common's
// DevopsComponentName, consumed by resolvePublishedDevopsDeploySpec). It is NOT
// the release name — that is <tenant>-devops. Mirrors the backend constant; if
// published charts ever become tenant-aware, update both together.
const PUBLISHED_RUNTIME_CHART_NAME = 'erun-devops';

// deployComponentLabel renders a user-facing label: the runtime item names its
// role, chart, and release (the operator does not manage the raw chart), while
// component charts show their chart name directly.
//
// The published-runtime case names the real chart (erun-devops) and the release
// name separately, because component.name there is only the Helm release name
// (<tenant>-devops): no per-tenant chart is published, so "<tenant>-devops
// (published)" wrongly implied a published <tenant>-devops chart and contradicted
// the erun-devops versions the picker offers. The local-chart case is
// left as-is — there component.name is a real repo-local chart directory the
// operator does manage.
export function deployComponentLabel(component: UIDeployableComponent): string {
  if (component.runtime) {
    if (component.source === 'published-chart') {
      if (component.name === PUBLISHED_RUNTIME_CHART_NAME) {
        return `Runtime — published ${PUBLISHED_RUNTIME_CHART_NAME} chart`;
      }
      return `Runtime — published ${PUBLISHED_RUNTIME_CHART_NAME} chart (released as ${component.name})`;
    }
    return `Runtime — ${component.name}`;
  }
  return component.name;
}
