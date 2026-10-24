import type { AppState } from './state';

type ManageDialog = AppState['manageDialog'];

export const RUNTIME_CHART_NOTICE_ID = 'environment-config-runtimechart-notice';

// Notice repeated inside the version panel, which covers the row beneath it.
export const RUNTIME_CHART_PANEL_NOTICE_ID = 'environment-config-runtimechart-notice-panel';

// runtimeChartBlocksDeploy reports that the registry says no chart exists at the
// picked version and the env states none; `unknown` never blocks.
export function runtimeChartBlocksDeploy(dialog: ManageDialog): boolean {
  const plan = dialog.runtimeChartPlan;
  if (!plan || !plan.missing || plan.unknown) {
    return false;
  }
  // The saved chart decides: an unsaved edit would not be used by a deploy.
  const initial = dialog.initialConfig;
  const effective = initial ? (initial.runtimeChart ?? '') : (dialog.config.runtimeChart ?? '');
  return effective.trim() === '';
}

export function runtimeChartUnsaved(dialog: ManageDialog): boolean {
  const initial = dialog.initialConfig;
  if (!initial) {
    return false;
  }
  return (dialog.config.runtimeChart ?? '').trim() !== (initial.runtimeChart ?? '').trim();
}

export function savedRuntimeChartLabel(dialog: ManageDialog): string {
  const saved = (dialog.initialConfig?.runtimeChart ?? '').trim();
  return saved === '' ? ' (the chart published with the deployed version)' : `, ${saved}`;
}

// statedRuntimeChart is the env's saved chart; null when none or while an edit is unsaved.
export function statedRuntimeChart(
  dialog: ManageDialog,
  deployVersion: string,
): { chart: string; version: string } | null {
  if (runtimeChartUnsaved(dialog)) {
    return null;
  }
  const reference = (dialog.config.runtimeChart ?? '').trim();
  if (reference === '') {
    return null;
  }
  const lastSlash = reference.lastIndexOf('/');
  const segment = lastSlash < 0 ? reference : reference.slice(lastSlash + 1);
  const separator = segment.lastIndexOf(':');
  if (separator <= 0) {
    return { chart: segment, version: deployVersion };
  }
  return { chart: segment.slice(0, separator), version: segment.slice(separator + 1) };
}
