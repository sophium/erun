import * as React from 'react';
import { RefreshCw, LoaderCircle } from 'lucide-react';

import type { ERunUIController } from '@/app/ERunUIController';
import type { AppState } from '@/app/state';
import type { UITenantDashboardBuild, UITenantDashboardReview, UITenantDashboardUser } from '@/types';
import { Button } from '@/components/ui/button';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';

export function TenantDashboardView({ controller, state }: { controller: ERunUIController; state: AppState }): React.ReactElement | null {
  const dashboard = state.tenantDashboard;
  if (!dashboard.tenant) {
    return null;
  }
  const tenant = state.tenants.find((candidate) => candidate.name === dashboard.tenant);
  return (
    <section className="grid h-full min-h-0 bg-background text-foreground">
      <div className="grid min-h-0 grid-rows-[auto_minmax(0,1fr)]">
        <header className="flex min-w-0 items-center justify-between border-b border-border px-5 py-4">
          <div className="min-w-0">
            <h1 className="truncate text-[20px] font-semibold leading-tight tracking-normal">{dashboard.tenant}</h1>
            <p className="truncate text-sm text-muted-foreground">{tenantDashboardSubtitle(tenant)}</p>
          </div>
          <Button type="button" variant="outline" size="sm" disabled={dashboard.loading} onClick={() => { void controller.loadTenantDashboard(); }}>
            {dashboard.loading ? <LoaderCircle className="animate-spin" aria-hidden="true" /> : <RefreshCw aria-hidden="true" />}
            Refresh
          </Button>
        </header>
        <Tabs value={dashboard.tab} onValueChange={(value) => controller.setTenantDashboardTab(value as AppState['tenantDashboard']['tab'])} className="grid min-h-0 grid-rows-[auto_minmax(0,1fr)] px-5 py-4">
          <TabsList className="w-fit">
            <TabsTrigger value="users">Users</TabsTrigger>
            <TabsTrigger value="queue">Merge queue</TabsTrigger>
            <TabsTrigger value="builds">Builds</TabsTrigger>
            <TabsTrigger value="audit">Audit log</TabsTrigger>
            <TabsTrigger value="api-log">API log</TabsTrigger>
          </TabsList>
          <TenantDashboardBody dashboard={dashboard} />
        </Tabs>
      </div>
    </section>
  );
}

function TenantDashboardBody({ dashboard }: { dashboard: AppState['tenantDashboard'] }): React.ReactElement {
  if (dashboard.loading) {
    return <DashboardMessage icon={<LoaderCircle className="animate-spin" aria-hidden="true" />} message="Loading tenant dashboard..." />;
  }
  if (dashboard.error) {
    return <DashboardMessage message={dashboard.error} destructive />;
  }
  return <TenantDashboardTabContent data={dashboard.data} />;
}

function TenantDashboardTabContent({ data }: { data: AppState['tenantDashboard']['data'] }): React.ReactElement {
  const view = tenantDashboardViewData(data);
  return (
    <>
      <TabsContent value="users" className="min-h-0 overflow-auto">
        <UsersTable users={view.users} apiError={view.apiError} />
      </TabsContent>
      <TabsContent value="queue" className="min-h-0 overflow-auto">
        <ReviewsTable reviews={view.mergeQueue} empty={view.apiError || 'No reviews are waiting in the merge queue'} destructive={view.hasAPIError} />
      </TabsContent>
      <TabsContent value="builds" className="min-h-0 overflow-auto">
        <BuildsTable builds={view.builds} apiError={view.apiError} />
      </TabsContent>
      <TabsContent value="audit" className="min-h-0 overflow-auto">
        <DashboardMessage message={view.auditLogMessage} />
      </TabsContent>
      <TabsContent value="api-log" className="min-h-0 overflow-auto">
        <APILogPanel log={view.apiLog} error={view.apiLogError} apiError={view.apiError} />
      </TabsContent>
    </>
  );
}

interface TenantDashboardViewData {
  users: UITenantDashboardUser[];
  apiError: string;
  hasAPIError: boolean;
  mergeQueue: UITenantDashboardReview[];
  builds: UITenantDashboardBuild[];
  auditLogMessage: string;
  apiLog: string;
  apiLogError: string;
}

const emptyTenantDashboardViewData: TenantDashboardViewData = {
  users: [],
  apiError: '',
  hasAPIError: false,
  mergeQueue: [],
  builds: [],
  auditLogMessage: 'No audit events',
  apiLog: '',
  apiLogError: '',
};

function tenantDashboardViewData(data: AppState['tenantDashboard']['data']): TenantDashboardViewData {
  if (!data) {
    return emptyTenantDashboardViewData;
  }
  const apiError = data.apiError ?? '';
  return {
    users: data.user ? [data.user] : [],
    apiError,
    hasAPIError: apiError.length > 0,
    mergeQueue: data.mergeQueue ?? [],
    builds: data.builds ?? [],
    auditLogMessage: data.auditLogMessage ?? 'No audit events',
    apiLog: data.apiLog ?? '',
    apiLogError: data.apiLogError ?? '',
  };
}

function UsersTable({ users, apiError }: { users: UITenantDashboardUser[]; apiError: string }): React.ReactElement {
  if (users.length === 0) {
    return <DashboardMessage message={apiError || 'No users found'} destructive={Boolean(apiError)} />;
  }
  return (
    <DataTable headers={['User', 'Username', 'Tenant', 'Issuer', 'Subject']}>
      {users.map((user) => (
        <tr key={user.userId}>
          <DataCell strong>{user.userId}</DataCell>
          <DataCell>{user.username}</DataCell>
          <DataCell>{user.tenantId}</DataCell>
          <DataCell>{user.issuer}</DataCell>
          <DataCell>{user.subject}</DataCell>
        </tr>
      ))}
    </DataTable>
  );
}

function ReviewsTable({ reviews, empty, destructive }: { reviews: UITenantDashboardReview[]; empty: string; destructive?: boolean }): React.ReactElement {
  if (reviews.length === 0) {
    return <DashboardMessage message={empty} destructive={destructive} />;
  }
  return (
    <DataTable headers={['Review', 'Status', 'Target', 'Source', 'Updated']}>
      {reviews.map((review) => (
        <tr key={review.reviewId}>
          <DataCell strong>{review.name || review.reviewId}</DataCell>
          <DataCell>{review.status}</DataCell>
          <DataCell>{review.targetBranch}</DataCell>
          <DataCell>{review.sourceBranch}</DataCell>
          <DataCell>{formatDate(review.updatedAt)}</DataCell>
        </tr>
      ))}
    </DataTable>
  );
}

function BuildsTable({ builds, apiError }: { builds: UITenantDashboardBuild[]; apiError: string }): React.ReactElement {
  if (builds.length === 0) {
    return <DashboardMessage message={apiError || 'No review builds found'} destructive={Boolean(apiError)} />;
  }
  return (
    <DataTable headers={['Build', 'Review', 'Result', 'Commit', 'Version', 'Created']}>
      {builds.map((build) => (
        <tr key={build.buildId}>
          <DataCell strong>{build.buildId}</DataCell>
          <DataCell>{build.reviewName || build.reviewId}</DataCell>
          <DataCell>{build.successful ? 'Successful' : 'Failed'}</DataCell>
          <DataCell>{build.commitId}</DataCell>
          <DataCell>{build.version}</DataCell>
          <DataCell>{formatDate(build.createdAt)}</DataCell>
        </tr>
      ))}
    </DataTable>
  );
}

function APILogPanel({ log, error, apiError }: { log: string; error: string; apiError: string }): React.ReactElement {
  if (error) {
    return <DashboardMessage message={error} destructive />;
  }
  if (!log.trim()) {
    return <DashboardMessage message={apiError || 'No API log returned'} destructive={Boolean(apiError)} />;
  }
  return (
    <>
      {apiError && <DashboardMessage message={apiError} destructive />}
      <pre className="mt-4 max-h-full overflow-auto rounded-[var(--radius)] border border-border bg-muted/30 px-3 py-2.5 font-mono text-xs leading-relaxed text-foreground whitespace-pre-wrap">
        {log}
      </pre>
    </>
  );
}

function DataTable({ headers, children }: { headers: string[]; children: React.ReactNode }): React.ReactElement {
  return (
    <table className="mt-4 w-full table-fixed border-collapse text-sm">
      <thead>
        <tr className="border-b border-border text-left text-xs font-medium uppercase text-muted-foreground">
          {headers.map((header) => <th key={header} className="px-2 py-2">{header}</th>)}
        </tr>
      </thead>
      <tbody className="divide-y divide-border">{children}</tbody>
    </table>
  );
}

function DataCell({ children, strong }: { children: React.ReactNode; strong?: boolean }): React.ReactElement {
  return <td className={`truncate px-2 py-2.5 ${strong ? 'font-medium' : 'text-muted-foreground'}`}>{children || '-'}</td>;
}

function DashboardMessage({ message, icon, destructive }: { message: string; icon?: React.ReactElement; destructive?: boolean }): React.ReactElement {
  return (
    <div className={`mt-4 flex items-center gap-2 rounded-[var(--radius)] border px-3 py-2.5 text-sm ${destructive ? 'border-destructive/35 text-destructive' : 'border-border text-muted-foreground'}`}>
      {icon}
      <span>{message}</span>
    </div>
  );
}

function tenantDashboardSubtitle(tenant: AppState['tenants'][number] | undefined): string {
  if (!tenant) {
    return 'Tenant dashboard';
  }
  const environmentCount = tenant.environments.length;
  const alias = tenant.primaryCloudProviderAlias?.trim();
  return `${environmentCount} environment${environmentCount === 1 ? '' : 's'}${alias ? `, primary ${alias}` : ''}`;
}

function formatDate(value: string | undefined): string {
  if (!value) {
    return '-';
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value;
  }
  return date.toLocaleString();
}
