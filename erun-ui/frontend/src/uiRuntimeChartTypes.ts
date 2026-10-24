import type { UIDeployableComponent } from './types';

// missing only on a positive registry answer; unreachable sets unknown, so a
// guess never blocks a deploy.
export interface UIRuntimeChartPlan {
  reference: string;
  version: string;
  chart: string;
  // 'stated' | 'tenant' | 'canonical' | 'local', or ''.
  source: string;
  missing: boolean;
  unknown: boolean;
}

export interface UIDeployComponentsResult {
  components: UIDeployableComponent[];
  runtimeChart: UIRuntimeChartPlan;
}
