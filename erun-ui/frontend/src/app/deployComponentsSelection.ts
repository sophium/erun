import type { UIDeployableComponent } from '@/types';

import type { AppState } from './state';

type ManageDialog = AppState['manageDialog'];

// Also rendered inside the picker, which is a modal covering the row.
export const DEPLOY_COMPONENTS_EMPTY_NOTICE_ID =
  'environment-config-deploy-components-empty-notice';
export const DEPLOY_COMPONENTS_EMPTY_PANEL_NOTICE_ID =
  'environment-config-deploy-components-empty-notice-panel';

// The Wails binding returns null for an empty slice.
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

export function deployComponentDefaultNames(options: UIDeployableComponent[]): string[] {
  return options.filter((option) => option.selected).map((option) => option.name);
}

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

// deployComponentsEmptySelection reports an explicitly emptied checklist,
// which blocks Deploy: an empty `--components` is omitted and would deploy the
// runtime chart alone. Before the checklist loads, emptiness is not a choice.
export function deployComponentsEmptySelection(dialog: ManageDialog): boolean {
  if (dialog.deployComponentsLoading || dialog.version.trim() === '') {
    return false;
  }
  if (dialog.deployComponents.length === 0) {
    return false;
  }
  return dialog.deployComponentSelection.length === 0;
}

// The release name is <tenant>-devops, not this. Mirrors the backend constant.
const PUBLISHED_RUNTIME_CHART_NAME = 'erun-devops';

// deployComponentLabel names the chart a deploy installs: the tenant's own
// <tenant>-devops when published at the version, else erun-devops.
export function deployComponentLabel(component: UIDeployableComponent): string {
  if (component.runtime) {
    if (component.source === 'published-chart') {
      const chart = component.publishedChart?.trim() ?? PUBLISHED_RUNTIME_CHART_NAME;
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
