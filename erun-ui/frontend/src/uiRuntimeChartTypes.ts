// Runtime-chart read-model types, kept beside types.ts the way
// ./uiDiagnosticsTypes is: one domain's shapes in their own module, imported by
// name where needed.

import type { UIDeployableComponent } from './types';

// UIRuntimeChartPlan is which chart a deploy at the chosen version would install
// for the runtime -- the coordinate the version picker does not name. missing is
// set only when the registry positively answered that no chart exists at that
// version; an unreachable or private registry sets unknown instead, so a guess
// never blocks a deploy that would have worked.
export interface UIRuntimeChartPlan {
  reference: string;
  version: string;
  chart: string;
  // 'stated' | 'tenant' | 'canonical' | 'local', or '' before anything is resolved.
  source: string;
  missing: boolean;
  unknown: boolean;
}

export interface UIDeployComponentsResult {
  components: UIDeployableComponent[];
  runtimeChart: UIRuntimeChartPlan;
}
