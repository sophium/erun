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

// Sidebar.EnvironmentRow.state.ts is the environment row's Redux read side,
// split out from the component so that file stays markup. The row now folds
// three derivations, not two: what the desktop is doing, what the environment
// reports about itself, and what the node under it was last observed to be.

// Each selector returns a primitive so React-Redux equality short-circuits
// row re-renders on unrelated slice churn.
function useEnvironmentRowSelectors(tenantName: string, environmentName: string) {
  const selectedSelection = useAppSelector((state) => state.selection.selected);
  const tenants = useAppSelector((state) => state.tenants.tenants);
  const isOpening = useAppSelector(
    (state) => state.sessions.openingByEnv[envKey(tenantName, environmentName)] === true,
  );
  // First running entry only, so the selector stays primitive-returning and
  // the activity slice's additive churn does not re-render every row.
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
  // Scope the busy indicator to THIS env so a reconnect/redeploy in the
  // review pane does not spin or lock the other rows.
  const reconnecting = useAppSelector(
    (state) =>
      state.review.reconnect.status === 'running' &&
      state.review.reconnect.tenant === tenantName &&
      state.review.reconnect.environment === environmentName,
  );
  // The env's real condition behind the open dot: '' running, 'stopped'
  // cloud context down, 'runtime-stopped' runtime scaled to zero, 'failed'
  // deploy or reconnect gave up.
  const envState = useAppSelector(
    (state) =>
      state.envStatus.statusByEnv[
        selectionKey({ tenant: tenantName, environment: environmentName })
      ] ?? '',
  );
  // What the environment itself reports, which is true whoever opened it — the
  // desktop, a CLI `erun open`, or an agent over MCP. Selectors stay primitive-
  // returning so an unchanged observation cannot re-render the row.
  const activityKey = selectionKey({ tenant: tenantName, environment: environmentName });
  const reachable = useAppSelector(
    (state) => state.envStatus.activityByEnv[activityKey]?.reachable === true,
  );
  // Whether the environment answered at all, which is a different question from
  // what it answered — see environmentRowIsBusy, where only an actual answer is
  // allowed to stop a row spinning.
  const envObserved = useAppSelector(
    (state) => state.envStatus.activityByEnv[activityKey]?.observed === true,
  );
  // Whether the environment lost the forward it had. Kept apart from reachable
  // because the two say different things: reachable is what the row already
  // believed, and this is the environment being unable to answer at all — the
  // port free after kubectl exited with its pod, or still bound with nothing
  // behind it.
  const envOutage = useAppSelector(
    (state) => state.envStatus.activityByEnv[activityKey]?.outage === true,
  );
  const envBusy = useAppSelector(
    (state) => state.envStatus.activityByEnv[activityKey]?.busy === true,
  );
  const envBusyDetail = useAppSelector(
    (state) => state.envStatus.activityByEnv[activityKey]?.detail ?? '',
  );
  // The usage sweep's cached reading for this env, if any — read straight from
  // the slice (not reduced to a primitive) since the hover card needs the
  // whole snapshot to render figures, age, and staleness together.
  const usage = useAppSelector((state) => state.envStatus.usageByEnv[activityKey]);
  // The cloud node behind this environment, read whole for the same reason
  // usage is: the indicator needs the name and the status together. An absent
  // entry is the definite "no node erun manages", not an unread one.
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

// useEnvironmentRowState folds the row's selectors and the two pure
// derivations over them into the one state the markup renders, so the component
// below stays markup.
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
  // The sidebar's single source of truth for "this row is the pane's focus"
  // — the tenant dashboard and an orchestrator's session both take priority
  // over an environment selection (see selectSidebarFocus), so a stale
  // selection.selected left over from before either took the pane can never
  // paint this row as selected too.
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
    // The node indicator is derived from the environment indicator's visibility,
    // never the other way round: an undetermined node is only worth saying on a
    // row that would otherwise say nothing at all, and no node reading may
    // rewrite what the environment reports about itself.
    nodeIndicator: environmentNodeIndicator({
      node,
      environmentIndicatorVisible: indicator.visible,
    }),
    usage,
    node,
  };
}
