import type { UIDeployableComponent } from '@/types';

import type { AppState } from './state';

type ManageDialog = AppState['manageDialog'];

// The empty-selection notice, rendered under the version row when the picker is
// closed, and again inside the picker panel -- the panel is a modal popover that
// covers the row beneath it, so the operator unchecking the last box would
// otherwise see the reason only after closing it.
export const DEPLOY_COMPONENTS_EMPTY_NOTICE_ID = 'environment-config-deploy-components-empty-notice';
export const DEPLOY_COMPONENTS_EMPTY_PANEL_NOTICE_ID =
  'environment-config-deploy-components-empty-notice-panel';

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

// deployComponentsEmptySelection reports that the checklist is on screen with
// every box unchecked -- an explicit statement the operator made, not silence.
//
// It blocks Deploy, because the resolution cannot tell the two apart: the desktop
// threads the selection to the CLI as `--components` only when it is non-empty,
// so an empty one reaches `erun deploy` looking exactly like an omitted flag and
// falls through the precedence tiers to the runtime chart alone (bootstrap/heal).
// Left enabled, Deploy would roll out the one chart the operator just declined,
// under a checklist promising exactly the checked charts.
//
// Scoped to a loaded, non-empty, version-scoped checklist deliberately. Before the
// probe answers, or when the registry offers nothing to check, an empty selection
// is emptiness of information rather than a choice: blocking on it would disable
// Deploy for a reason the operator never made, and would strand the health check's
// "runtime not deployed" recovery, which deploys the runtime on purpose.
export function deployComponentsEmptySelection(dialog: ManageDialog): boolean {
  if (dialog.deployComponentsLoading || dialog.version.trim() === '') {
    return false;
  }
  if (dialog.deployComponents.length === 0) {
    return false;
  }
  return dialog.deployComponentSelection.length === 0;
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
