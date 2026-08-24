import * as React from 'react';

import type { AppState } from '@/app/state';
import { formatDashboardDate, tenantDashboardPanel } from '@/app/tenantDashboardPanels';
import { TabsContent } from '@/components/ui/tabs';
import type {
  UITenantDashboardAudit,
  UITenantDashboardBuild,
  UITenantDashboardReview,
  UITenantDashboardUser,
} from '@/types';

import { EmptyState } from './EmptyState';
import { StatusBadge } from './StatusBadge';
import { DashboardMessage, DataCell, DataTable } from './TenantDashboardMessage';

type TenantDashboardData = AppState['tenantDashboard']['data'];

export function TenantDashboardPanels({ data }: { data: TenantDashboardData }): React.ReactElement {
  return (
    <>
      <UsersPanel data={data} />
      <MergeQueuePanel data={data} />
      <BuildsPanel data={data} />
      <AuditPanel data={data} />
      <TabsContent value="api-log" className="min-h-0 overflow-auto">
        <APILogPanel log={data?.apiLog ?? ''} error={data?.apiLogError ?? ''} />
      </TabsContent>
    </>
  );
}

function UsersPanel({ data }: { data: TenantDashboardData }): React.ReactElement {
  const user = data?.user;
  return (
    <TabsContent value="users" className="min-h-0 overflow-auto">
      <PanelBody data={data} tab="users" empty={<EmptyState heading="No signed-in user" />}>
        {user ? <UsersTable users={[user]} /> : null}
      </PanelBody>
    </TabsContent>
  );
}

function MergeQueuePanel({ data }: { data: TenantDashboardData }): React.ReactElement {
  const mergeQueue = data?.mergeQueue ?? [];
  return (
    <TabsContent value="queue" className="min-h-0 overflow-auto">
      <PanelBody
        data={data}
        tab="queue"
        empty={
          <EmptyState
            heading="No reviews are waiting in the merge queue"
            body="A review joins the queue once a build has passed for it."
          />
        }
      >
        {mergeQueue.length > 0 ? <ReviewsTable reviews={mergeQueue} /> : null}
      </PanelBody>
    </TabsContent>
  );
}

function BuildsPanel({ data }: { data: TenantDashboardData }): React.ReactElement {
  const builds = data?.builds ?? [];
  return (
    <TabsContent value="builds" className="min-h-0 overflow-auto">
      <PanelBody
        data={data}
        tab="builds"
        empty={
          <EmptyState
            heading="No review builds yet"
            body="Builds appear here as they are recorded against this tenant's reviews."
          />
        }
      >
        {builds.length > 0 ? <BuildsTable builds={builds} /> : null}
      </PanelBody>
    </TabsContent>
  );
}

function AuditPanel({ data }: { data: TenantDashboardData }): React.ReactElement {
  const events = data?.auditEvents ?? [];
  return (
    <TabsContent value="audit" className="min-h-0 overflow-auto">
      <PanelBody
        data={data}
        tab="audit"
        empty={
          <EmptyState
            heading="No audit events"
            body="Tenant activity will appear here as API, CLI, and MCP calls are made."
          />
        }
      >
        {events.length > 0 ? <AuditEventsTable events={events} /> : null}
      </PanelBody>
    </TabsContent>
  );
}

// PanelBody renders one panel's three distinguishable outcomes: it failed, there
// is nothing in it, or here it is. "You may not read this" never reaches here —
// a restricted panel has no tab to open.
function PanelBody({
  data,
  tab,
  empty,
  children,
}: {
  data: TenantDashboardData;
  tab: 'users' | 'queue' | 'builds' | 'audit';
  empty: React.ReactElement;
  children: React.ReactNode;
}): React.ReactElement {
  const panel = tenantDashboardPanel(data, tab);
  if (panel?.restricted) {
    return (
      <div className="mt-4">
        <EmptyState
          heading="You do not have access to this panel"
          body={`It needs ${panel.restricted}. Ask an administrator for access.`}
        />
      </div>
    );
  }
  if (panel?.error) {
    return <DashboardMessage message={panel.error} destructive />;
  }
  if (!children) {
    return <div className="mt-4">{empty}</div>;
  }
  return <>{children}</>;
}

function UsersTable({ users }: { users: UITenantDashboardUser[] }): React.ReactElement {
  return (
    <DataTable headers={['Username', 'Roles']}>
      {users.map((user) => (
        <tr key={user.userId || (user.username ?? '') || (user.subject ?? '')}>
          <DataCell strong>{displayUsername(user)}</DataCell>
          <DataCell>{formatRoles(user.roles)}</DataCell>
        </tr>
      ))}
    </DataTable>
  );
}

function ReviewsTable({ reviews }: { reviews: UITenantDashboardReview[] }): React.ReactElement {
  return (
    <DataTable headers={['Review', 'Status', 'Target', 'Source', 'Updated']}>
      {reviews.map((review) => (
        <tr key={review.reviewId}>
          <DataCell strong>{review.name || review.reviewId}</DataCell>
          <DataCell>{review.status}</DataCell>
          <DataCell>{review.targetBranch}</DataCell>
          <DataCell>{review.sourceBranch}</DataCell>
          <DataCell>{formatDashboardDate(review.updatedAt)}</DataCell>
        </tr>
      ))}
    </DataTable>
  );
}

function BuildsTable({ builds }: { builds: UITenantDashboardBuild[] }): React.ReactElement {
  return (
    <DataTable headers={['Build', 'Review', 'Result', 'Commit', 'Version', 'Created']}>
      {builds.map((build) => (
        <tr key={build.buildId}>
          <DataCell strong>{build.buildId}</DataCell>
          <DataCell>{build.reviewName?.trim() ? build.reviewName : build.reviewId}</DataCell>
          <DataCell>
            <StatusBadge
              tone={build.successful ? 'success' : 'destructive'}
              label={build.successful ? 'Successful' : 'Failed'}
            />
          </DataCell>
          <DataCell>{build.commitId}</DataCell>
          <DataCell>{build.version}</DataCell>
          <DataCell>{formatDashboardDate(build.createdAt)}</DataCell>
        </tr>
      ))}
    </DataTable>
  );
}

function AuditEventsTable({ events }: { events: UITenantDashboardAudit[] }): React.ReactElement {
  return (
    <DataTable headers={['Time', 'Type', 'Actor', 'Action']}>
      {events.map((event, index) => (
        <tr key={`${event.createdAt ?? ''}-${String(index)}`}>
          <DataCell>{formatDashboardDate(event.createdAt)}</DataCell>
          <DataCell>{event.type}</DataCell>
          <DataCell>{event.actor}</DataCell>
          <DataCell strong>{event.action}</DataCell>
        </tr>
      ))}
    </DataTable>
  );
}

function APILogPanel({ log, error }: { log: string; error: string }): React.ReactElement {
  if (error) {
    return <DashboardMessage message={error} destructive />;
  }
  if (!log.trim()) {
    return (
      <div className="mt-4">
        <EmptyState
          heading="No API log returned"
          body="The environment's API container has not logged anything yet."
        />
      </div>
    );
  }
  return (
    <pre className="mt-4 max-h-full overflow-auto rounded-[var(--radius)] border border-border bg-muted/30 px-3 py-2.5 font-mono text-xs leading-relaxed text-foreground whitespace-pre-wrap">
      {log}
    </pre>
  );
}

function displayUsername(user: UITenantDashboardUser): string {
  const candidates = [user.username, user.subject, user.userId];
  for (const candidate of candidates) {
    const trimmed = candidate?.trim() ?? '';
    if (trimmed !== '') {
      return trimmed;
    }
  }
  return 'Unknown user';
}

function formatRoles(roles: string[] | undefined): string {
  const names = roles?.map((role) => role.trim()).filter(Boolean) ?? [];
  return names.length > 0 ? names.join(', ') : 'No roles assigned';
}
