import { Button, Tabs, TabsList, TabsTrigger } from 'erun-kit';
import { LoaderCircle, RefreshCw } from 'lucide-react';
import * as React from 'react';

import { useAppDispatch, useAppSelector } from '@/app/hooks';
import type { AppState } from '@/app/state';
import {
  activeTenantDashboardTab,
  restrictedTenantDashboardReads,
  visibleTenantDashboardTabs,
} from '@/app/tenantDashboardPanels';
import {
  loadTenantDashboard,
  refreshTenantDashboard,
  setTenantDashboardTab,
} from '@/app/tenantDialogThunks';
import type { UITenant } from '@/types';

import { PlatformErrorAlert } from './PlatformSignInAlert';
import { DashboardMessage } from './TenantDashboardMessage';
import { TenantDashboardPanels } from './TenantDashboardPanels';
export function TenantDashboardView(): React.ReactElement | null {
  const dispatch = useAppDispatch();
  const dashboard = useAppSelector((state) => state.tenantDashboard);
  const tenants = useAppSelector((state) => state.tenants.tenants);
  if (!dashboard.tenant) {
    return null;
  }
  const tenant = tenants.find((candidate) => candidate.name === dashboard.tenant);
  const cloudProviderAlias = tenant?.primaryCloudProviderAlias?.trim() ?? '';
  const environmentName = tenantDashboardEnvironmentName(tenant, dashboard.data?.environment);
  const visibleTabs = visibleTenantDashboardTabs(dashboard.data);
  return (
    <section className="grid h-full min-h-0 bg-background text-foreground">
      <div className="grid min-h-0 grid-rows-[auto_minmax(0,1fr)]">
        <header className="flex min-w-0 items-center justify-between border-b border-border px-5 py-4">
          <div className="min-w-0">
            <h1 className="truncate text-[20px] font-semibold leading-tight tracking-normal">
              {dashboard.tenant}
            </h1>
            <p className="truncate text-sm text-muted-foreground">
              {tenantDashboardSubtitle(tenant, environmentName)}
            </p>
          </div>
          <Button
            type="button"
            variant="outline"
            size="sm"
            disabled={dashboard.loading}
            onClick={() => {
              void dispatch(refreshTenantDashboard());
            }}
          >
            {dashboard.loading ? (
              <LoaderCircle className="animate-spin" aria-hidden="true" />
            ) : (
              <RefreshCw aria-hidden="true" />
            )}
            Refresh
          </Button>
        </header>
        <Tabs
          value={activeTenantDashboardTab(dashboard.data, dashboard.tab)}
          onValueChange={(value) => {
            dispatch(setTenantDashboardTab(value as AppState['tenantDashboard']['tab']));
          }}
          className="grid min-h-0 grid-rows-[auto_minmax(0,1fr)] px-5 py-4"
        >
          <div className="grid gap-2">
            <TabsList className="w-fit">
              {visibleTabs.map((descriptor) => (
                <TabsTrigger key={descriptor.tab} value={descriptor.tab}>
                  {descriptor.label}
                </TabsTrigger>
              ))}
            </TabsList>
            <RestrictedAccessNote missing={restrictedTenantDashboardReads(dashboard.data)} />
          </div>
          <TenantDashboardBody dashboard={dashboard} cloudProviderAlias={cloudProviderAlias} />
        </Tabs>
      </div>
    </section>
  );
}

function TenantDashboardBody({
  dashboard,
  cloudProviderAlias,
}: {
  dashboard: AppState['tenantDashboard'];
  cloudProviderAlias: string;
}): React.ReactElement {
  if (dashboard.loading) {
    return (
      <DashboardMessage
        icon={<LoaderCircle className="animate-spin" aria-hidden="true" />}
        message="Loading tenant dashboard..."
      />
    );
  }
  if (dashboard.error) {
    return <DashboardMessage message={dashboard.error} destructive />;
  }
  const apiError = dashboard.data?.apiError ?? '';
  if (apiError) {
    return <TenantDashboardFailure message={apiError} alias={cloudProviderAlias} />;
  }
  return <TenantDashboardPanels data={dashboard.data} />;
}

// TenantDashboardFailure is the whole-dashboard failure's own layout: centered
// in the panel's full height instead of DashboardMessage's small top-anchored
// row, which used to leave the message adrift over a large empty area beneath
// it (#1390). Carries the sign-in action when the failure is a stale identity.
function TenantDashboardFailure({
  message,
  alias,
}: {
  message: string;
  alias: string;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  return (
    <div className="flex h-full min-h-0 items-center justify-center">
      <div className="w-full max-w-sm">
        <PlatformErrorAlert
          message={message}
          alias={alias}
          onRecovered={() => {
            void dispatch(loadTenantDashboard());
          }}
        />
      </div>
    </div>
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
  const alias = tenant.primaryCloudProviderAlias?.trim();
  const parts = [
    environmentName,
    `${String(environmentCount)} environment${environmentCount === 1 ? '' : 's'}`,
    alias ? `Primary cloud: ${alias}` : '',
  ].filter(Boolean);
  return parts.join(', ');
}

function tenantDashboardEnvironmentName(
  tenant: UITenant | undefined,
  loadedEnvironment: string | undefined,
): string {
  const environmentName = loadedEnvironment?.trim();
  if (environmentName) {
    return environmentName;
  }
  if (!tenant) {
    return '';
  }
  const defaultEnvironment = tenant.defaultEnvironment?.trim();
  const environment =
    tenant.environments.find(
      (candidate) => candidate.name === defaultEnvironment && candidate.apiUrl,
    ) ?? tenant.environments.find((candidate) => candidate.apiUrl);
  return environment?.name.trim() ?? '';
}
