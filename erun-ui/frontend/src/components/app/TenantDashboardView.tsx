import { Button, Tabs, TabsList, TabsTrigger } from 'erun-kit';
import { LoaderCircle, RefreshCw } from 'lucide-react';
import * as React from 'react';

import { useAppDispatch, useAppSelector } from '@/app/hooks';
import type { AppState } from '@/app/state';
import {
  activeTenantDashboardTab,
  requestsTabLabel,
  restrictedTenantDashboardReads,
  tenantDashboardEnvironmentName,
  visibleTenantDashboardTabs,
} from '@/app/tenantDashboardPanels';
import {
  loadTenantDashboard,
  refreshTenantDashboard,
  setTenantDashboardTab,
} from '@/app/tenantDialogThunks';
import type { UITenant } from '@/types';

import { InlineAlert } from './InlineAlert';
import { DashboardMessage } from './TenantDashboardMessage';
import { TenantDashboardPanels } from './TenantDashboardPanels';
import { TenantPlatformStateCard } from './TenantPlatformState';

// tenantDashboardIsBlocked reports whether the surface has a single shared
// precondition unmet (loading, a generic load failure, or one of the
// platform-readiness states) — in which case the tab strip and per-panel
// chrome must not render at all: a caller who cannot read anything sees
// that named once, not six enabled tabs each discovering it on their own.
function tenantDashboardIsBlocked(dashboard: AppState['tenantDashboard']): boolean {
  return (
    dashboard.loading ||
    Boolean(dashboard.error) ||
    Boolean(dashboard.data?.platformState) ||
    Boolean(dashboard.data?.apiError && !dashboard.data.platformState)
  );
}

export function TenantDashboardView(): React.ReactElement | null {
  const dispatch = useAppDispatch();
  const dashboard = useAppSelector((state) => state.tenantDashboard);
  const tenants = useAppSelector((state) => state.tenants.tenants);
  if (!dashboard.tenant) {
    return null;
  }
  const tenant = tenants.find((candidate) => candidate.name === dashboard.tenant);
  const environmentName = tenantDashboardEnvironmentName(tenant, dashboard.data?.environment);
  const blocked = tenantDashboardIsBlocked(dashboard);
  return (
    <section className="grid h-full min-h-0 min-w-0 grid-cols-[minmax(0,1fr)] bg-background text-foreground">
      <div className="grid min-h-0 min-w-0 grid-cols-[minmax(0,1fr)] grid-rows-[auto_minmax(0,1fr)]">
        <header className="flex min-w-0 items-center justify-between border-b border-border px-5 py-4">
          <div className="min-w-0">
            <h1 className="truncate text-[20px] font-semibold leading-tight tracking-normal">
              {dashboard.tenant}
            </h1>
            <p className="truncate text-sm text-muted-foreground">
              {tenantDashboardSubtitle(tenant, environmentName)}
            </p>
          </div>
          {/* Refresh is a manual-choice convenience for an already-loaded
              dashboard, never the repair path for a blocked one — every
              blocked state recovers on its own once its real precondition
              is resolved. */}
          {!blocked && (
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => {
                void dispatch(refreshTenantDashboard());
              }}
            >
              <RefreshCw aria-hidden="true" />
              Refresh
            </Button>
          )}
        </header>
        {blocked ? (
          <TenantDashboardBlockedBody dashboard={dashboard} tenant={dashboard.tenant} />
        ) : (
          <TenantDashboardReadyBody dashboard={dashboard} />
        )}
      </div>
    </section>
  );
}

// TenantDashboardBlockedBody renders the one shared reason nothing loaded,
// full-panel, with no tab strip beside it: the operator sees a designed
// card naming what was attempted, not an alert floating over an
// otherwise-empty tab strip.
function TenantDashboardBlockedBody({
  dashboard,
  tenant,
}: {
  dashboard: AppState['tenantDashboard'];
  tenant: string;
}): React.ReactElement {
  return (
    <div className="grid min-h-0 place-items-center px-5 py-4">
      <div className="w-full max-w-lg">
        <TenantDashboardBlockedCard dashboard={dashboard} tenant={tenant} />
      </div>
    </div>
  );
}

function TenantDashboardBlockedCard({
  dashboard,
  tenant,
}: {
  dashboard: AppState['tenantDashboard'];
  tenant: string;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  if (dashboard.loading) {
    return (
      <DashboardMessage
        icon={<LoaderCircle className="animate-spin" aria-hidden="true" />}
        message={`Loading ${tenant}'s dashboard…`}
      />
    );
  }
  if (dashboard.error) {
    return (
      <GenericLoadFailure
        message={dashboard.error}
        onRetry={() => {
          void dispatch(loadTenantDashboard());
        }}
      />
    );
  }
  if (dashboard.data?.platformState) {
    return <TenantPlatformStateCard data={dashboard.data} />;
  }
  // A whole-dashboard apiError with no platformState is a failure this build
  // could not classify into one of the named states — diagnosed as far as
  // possible, not guessed the rest of the way (per the standard's rule 1).
  return (
    <GenericLoadFailure
      message={dashboard.data?.apiError ?? 'The tenant dashboard failed to load.'}
      onRetry={() => {
        void dispatch(loadTenantDashboard());
      }}
    />
  );
}

function GenericLoadFailure({
  message,
  onRetry,
}: {
  message: string;
  onRetry: () => void;
}): React.ReactElement {
  return (
    <div className="grid gap-3">
      <InlineAlert>{message}</InlineAlert>
      <Button
        type="button"
        variant="outline"
        size="sm"
        className="justify-self-start"
        onClick={onRetry}
      >
        <RefreshCw aria-hidden="true" />
        Try again
      </Button>
    </div>
  );
}

function TenantDashboardReadyBody({
  dashboard,
}: {
  dashboard: AppState['tenantDashboard'];
}): React.ReactElement {
  const dispatch = useAppDispatch();
  const visibleTabs = visibleTenantDashboardTabs(dashboard.data);
  return (
    <Tabs
      value={activeTenantDashboardTab(dashboard.data, dashboard.tab)}
      onValueChange={(value) => {
        dispatch(setTenantDashboardTab(value as AppState['tenantDashboard']['tab']));
      }}
      className="grid min-h-0 min-w-0 grid-cols-[minmax(0,1fr)] grid-rows-[auto_minmax(0,1fr)] px-5 py-4"
    >
      <div className="grid min-w-0 grid-cols-[minmax(0,1fr)] gap-2">
        {/* The strip wraps rather than truncating: a narrow <main> has less
            width than the full tab set needs, and a tab an operator cannot
            reach is a worse answer than a second line.

            The height override has to carry the primitive's own variant, not
            a bare h-auto. The primitive pins the height under
            group-data-[orientation=horizontal]/tabs, which both survives
            tailwind-merge beside an unqualified class and outranks it on
            specificity — so the tabs wrapped while their box stayed one row
            tall, and the overflowing row painted over the panel beneath it.

            flex-none likewise overrides the primitive's flex-1: a grown tab
            fills whatever its row has left, which turns a lone wrapped tab
            into a full-width bar sitting in dead space instead of a tab. */}
        <TabsList className="group-data-[orientation=horizontal]/tabs:h-auto w-full flex-wrap justify-start gap-1">
          {visibleTabs.map((descriptor) => (
            <TabsTrigger key={descriptor.tab} value={descriptor.tab} className="h-auto flex-none">
              {descriptor.tab === 'requests' ? requestsTabLabel(dashboard.data) : descriptor.label}
            </TabsTrigger>
          ))}
        </TabsList>
        <RestrictedAccessNote missing={restrictedTenantDashboardReads(dashboard.data)} />
      </div>
      <TenantDashboardPanels data={dashboard.data} />
    </Tabs>
  );
}

// RestrictedAccessNote is why a short tab strip is not an empty tenant. Hiding
// the tabs alone would leave the user to infer that the panels do not exist, so
// the access they are missing is named where they are looking (Nielsen #1).
function RestrictedAccessNote({ missing }: { missing: string[] }): React.ReactElement | null {
  if (missing.length === 0) {
    return null;
  }
  return (
    <p className="text-[13px] leading-[1.4] text-muted-foreground" role="status">
      {`Some panels are hidden because you do not have access to ${missing.join(', ')}. Ask an administrator for access.`}
    </p>
  );
}

function tenantDashboardSubtitle(tenant: UITenant | undefined, environmentName: string): string {
  if (!tenant) {
    return environmentName || 'Tenant dashboard';
  }
  const environmentCount = tenant.environments.length;
  const parts = [
    environmentName,
    `${String(environmentCount)} environment${environmentCount === 1 ? '' : 's'}`,
  ].filter(Boolean);
  return parts.join(', ');
}
