import { Button, Tabs, TabsList, TabsTrigger } from 'erun-kit';
import { LoaderCircle, RefreshCw } from 'lucide-react';
import * as React from 'react';

import { accessRemedyFor } from '@/app/accessRemedies';
import { useAppDispatch, useAppSelector } from '@/app/hooks';
import type { AppState } from '@/app/state';
import {
  activeTenantDashboardTab,
  requestsTabLabel,
  restrictedTenantDashboardReads,
  tenantDashboardEnvironmentName,
  tenantDashboardPlatformTenantName,
  visibleTenantDashboardTabs,
} from '@/app/tenantDashboardPanels';
import {
  loadTenantDashboard,
  refreshTenantDashboard,
  setTenantDashboardTab,
} from '@/app/tenantDialogThunks';
import type { UIAccessRemedy, UITenant } from '@/types';

import { AccessRemedyNote } from './AccessRemedyNote';
import { InlineAlert } from './InlineAlert';
import { DashboardMessage } from './TenantDashboardMessage';
import { TenantDashboardPanels } from './TenantDashboardPanels';
import { TenantPlatformStateCard } from './TenantPlatformState';

// A blocked dashboard renders its one shared reason without the tab strip.
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
  const platformTenantName = tenantDashboardPlatformTenantName(dashboard.data);
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
            {/* The local tenant and the platform tenant may differ; this names the latter. */}
            {platformTenantName && (
              <p className="truncate text-[12px] text-muted-foreground">
                Platform tenant: <span className="font-mono">{platformTenantName}</span>
              </p>
            )}
          </div>
          {/* Blocked states recover on their own, so Refresh is only offered when loaded. */}
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
  // An apiError with no platformState is a failure this build could not classify.
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
        {/* Wrap rather than truncate. The height override must carry the primitive's
            group-data variant to outrank its pinned height; flex-none overrides
            flex-1 so a wrapped tab does not stretch full-width. */}
        <TabsList className="group-data-[orientation=horizontal]/tabs:h-auto w-full flex-wrap justify-start gap-1">
          {visibleTabs.map((descriptor) => (
            <TabsTrigger key={descriptor.tab} value={descriptor.tab} className="h-auto flex-none">
              {descriptor.tab === 'requests' ? requestsTabLabel(dashboard.data) : descriptor.label}
            </TabsTrigger>
          ))}
        </TabsList>
        <RestrictedAccessNote
          missing={restrictedTenantDashboardReads(dashboard.data)}
          remedies={dashboard.data?.accessRemedies}
        />
      </div>
      <TenantDashboardPanels data={dashboard.data} />
    </Tabs>
  );
}

// RestrictedAccessNote names the missing access so a short tab strip does not read as an empty tenant.
function RestrictedAccessNote({
  missing,
  remedies,
}: {
  missing: string[];
  remedies?: Record<string, UIAccessRemedy>;
}): React.ReactElement | null {
  if (missing.length === 0) {
    return null;
  }
  return (
    <div className="text-[13px] leading-[1.4] text-muted-foreground" role="status">
      <p>{`Some panels are hidden because you do not have access to ${missing.join(', ')}. Ask an administrator for access.`}</p>
      {missing.map((read) => (
        <AccessRemedyNote key={read} remedy={accessRemedyFor(remedies, read)} />
      ))}
    </div>
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
