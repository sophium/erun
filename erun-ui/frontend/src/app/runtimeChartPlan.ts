import type { AppState } from './state';

type ManageDialog = AppState['manageDialog'];

export const RUNTIME_CHART_NOTICE_ID = 'environment-config-runtimechart-notice';

// The same statement, rendered inside the version panel while it is open -- the
// panel is a modal popover that covers the row beneath it, so the operator would
// otherwise choose a version with the reason it cannot be deployed hidden behind
// the thing they are choosing in.
export const RUNTIME_CHART_PANEL_NOTICE_ID = 'environment-config-runtimechart-notice-panel';

// runtimeChartBlocksDeploy reports that the picked version cannot be deployed as
// it stands: the registry answered that no runtime chart exists at it, and the
// environment states no chart of its own. Deploy is disabled on this, because the
// chart pull is known to fail -- letting the operator start a rollout that cannot
// succeed is worse than saying so first. An unreachable or private registry
// answers `unknown` and never blocks.
export function runtimeChartBlocksDeploy(dialog: ManageDialog): boolean {
  const plan = dialog.runtimeChartPlan;
  if (!plan || !plan.missing || plan.unknown) {
    return false;
  }
  // The *saved* chart decides, because that is the one a deploy would install --
  // an edit still on screen would not be used, so leaving Deploy enabled on it
  // would start a rollout that fails for the reason the operator just fixed but
  // has not saved. The unsaved warning names that, and this keeps the button
  // honest about it.
  const initial = dialog.initialConfig;
  const effective = initial ? (initial.runtimeChart ?? '') : (dialog.config.runtimeChart ?? '');
  return effective.trim() === '';
}

// runtimeChartUnsaved reports that the chart on screen is not the chart a deploy
// would use. Every field in this dialog takes effect on save, so the panel must
// not imply otherwise while an edit is pending.
export function runtimeChartUnsaved(dialog: ManageDialog): boolean {
  const initial = dialog.initialConfig;
  if (!initial) {
    return false;
  }
  return (dialog.config.runtimeChart ?? '').trim() !== (initial.runtimeChart ?? '').trim();
}

// savedRuntimeChartLabel names the chart a deploy would install right now, for the
// unsaved-edit warning: either the environment's saved reference, or the paired
// default in words rather than as an empty string.
export function savedRuntimeChartLabel(dialog: ManageDialog): string {
  const saved = (dialog.initialConfig?.runtimeChart ?? '').trim();
  return saved === '' ? ' (the chart published with the deployed version)' : `, ${saved}`;
}

// statedRuntimeChart is the environment's own saved chart, split into the chart
// name and the version it would install at, for the status line. Null when the env
// states none, or while an edit is still unsaved -- there the unsaved warning
// speaks instead.
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
