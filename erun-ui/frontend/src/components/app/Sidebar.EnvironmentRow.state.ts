import {
  type EnvironmentNodeIndicator,
  environmentNodeIndicator,
} from '@/app/environmentNodeState';
import { useAppSelector } from '@/app/hooks';
import { selectSidebarFocus } from '@/app/selectors';
import { envKey } from '@/app/slices/sessionsSlice';
import { selectionKey } from '@/app/versionSuggestions';
import {
  deriveEnvironmentRow,
  type EnvironmentIndicator,
  environmentIndicator,
  type EnvironmentRowDerived,
} from '@/components/app/Sidebar.helpers';
import type { UIEnvironmentNodeSnapshot } from '@/uiEnvironmentNodeTypes';
import type { UIEnvironmentUsageSnapshot } from '@/uiEnvironmentUsageTypes';

// Primitive-returning selectors keep unrelated slice churn from re-rendering rows.
function useEnvironmentRowSelectors(tenantName: string, environmentName: string) {
  const selectedSelection = useAppSelector((state) => state.selection.selected);
  const tenants = useAppSelector((state) => state.tenants.tenants);
  const isOpening = useAppSelector(
    (state) => state.sessions.openingByEnv[envKey(tenantName, environmentName)] === true,
  );
  // First running entry only, to stay primitive-returning.
  const runningCommand = useAppSelector((state) => {
    for (const entry of state.activity.entries) {
      if (
        entry.tenant === tenantName &&
        entry.environment === environmentName &&
        entry.status === 'running'
      ) {
        return entry.command;
      }
    }
    return '';
  });
  const aiBusy = useAppSelector(
    (state) =>
      state.aiActivity.aiBusyByEnv[
        selectionKey({ tenant: tenantName, environment: environmentName })
      ] === true,
  );
  const isOpen = useAppSelector((state) => {
    const key = selectionKey({ tenant: tenantName, environment: environmentName });
    return (state.terminal.tabsByEnv[key]?.length ?? 0) > 0;
  });
  // Scoped to this env so other rows do not spin.
  const reconnecting = useAppSelector(
    (state) =>
      state.review.reconnect.status === 'running' &&
      state.review.reconnect.tenant === tenantName &&
      state.review.reconnect.environment === environmentName,
  );
  // '' running, 'stopped' cloud context down, 'runtime-stopped' scaled to zero, 'failed' gave up.
  const envState = useAppSelector(
    (state) =>
      state.envStatus.statusByEnv[
        selectionKey({ tenant: tenantName, environment: environmentName })
      ] ?? '',
  );
  // What the env itself reports, whoever opened it.
  const activityKey = selectionKey({ tenant: tenantName, environment: environmentName });
  const reachable = useAppSelector(
    (state) => state.envStatus.activityByEnv[activityKey]?.reachable === true,
  );
  // Whether it answered at all; only an answer stops the row spinning (environmentRowIsBusy).
  const envObserved = useAppSelector(
    (state) => state.envStatus.activityByEnv[activityKey]?.observed === true,
  );
  // Lost its forward entirely, distinct from reachable.
  const envOutage = useAppSelector(
    (state) => state.envStatus.activityByEnv[activityKey]?.outage === true,
  );
  const envBusy = useAppSelector(
    (state) => state.envStatus.activityByEnv[activityKey]?.busy === true,
  );
  const envBusyDetail = useAppSelector(
    (state) => state.envStatus.activityByEnv[activityKey]?.detail ?? '',
  );
  // Read whole: the hover card needs figures, age and staleness together.
  const usage = useAppSelector((state) => state.envStatus.usageByEnv[activityKey]);
  // Read whole; absent means no erun-managed node, not unread.
  const node = useAppSelector((state) => state.envStatus.nodeByEnv[activityKey]);
  return {
    selectedSelection,
    tenants,
    isOpening,
    runningCommand,
    aiBusy,
    isOpen,
    reconnecting,
    envState,
    reachable,
    envObserved,
    envOutage,
    envBusy,
    envBusyDetail,
    usage,
    node,
  };
}

// useEnvironmentRowState folds the selectors and derivations into the rendered state.
export function useEnvironmentRowState(
  tenantName: string,
  environmentName: string,
): EnvironmentRowDerived & {
  envState: string;
  indicator: EnvironmentIndicator;
  nodeIndicator: EnvironmentNodeIndicator;
  usage: UIEnvironmentUsageSnapshot | undefined;
  node: UIEnvironmentNodeSnapshot | undefined;
} {
  const {
    selectedSelection,
    tenants,
    isOpening,
    runningCommand,
    aiBusy,
    isOpen,
    reconnecting,
    envState,
    reachable,
    envObserved,
    envOutage,
    envBusy,
    envBusyDetail,
    usage,
    node,
  } = useEnvironmentRowSelectors(tenantName, environmentName);
  const derived = deriveEnvironmentRow(
    tenantName,
    environmentName,
    selectedSelection,
    tenants,
    isOpening,
    runningCommand,
    aiBusy,
    reconnecting,
    envBusy,
    envBusyDetail,
    envObserved,
  );
  // selectSidebarFocus lets the dashboard and orchestrator sessions outrank a stale env selection.
  const focus = useAppSelector(selectSidebarFocus);
  const indicator = environmentIndicator({
    name: `${tenantName} / ${environmentName}`,
    envState,
    isOpen,
    reachable,
    outage: envOutage,
    busy: envBusy,
    detail: envBusyDetail,
  });
  return {
    ...derived,
    selected:
      focus.kind === 'environment' &&
      focus.tenant === tenantName &&
      focus.environment === environmentName,
    envState,
    indicator,
    // Derived from the env indicator's visibility, never the reverse.
    nodeIndicator: environmentNodeIndicator({
      node,
      environmentIndicatorVisible: indicator.visible,
    }),
    usage,
    node,
  };
}
