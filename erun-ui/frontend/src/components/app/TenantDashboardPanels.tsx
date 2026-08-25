import { Button, EmptyState, StatusBadge, TabsContent } from 'erun-kit';
import { LoaderCircle, Plus } from 'lucide-react';
import * as React from 'react';

import { openCreateReviewDialog } from '@/app/createReviewDialogThunks';
import { useAppDispatch, useAppSelector } from '@/app/hooks';
import {
  cancelAdvanceMergeQueue,
  confirmAdvanceMergeQueue,
  submitAdvanceMergeQueue,
} from '@/app/mergeQueueThunks';
import { openReviewDetail } from '@/app/reviewDetailThunks';
import type { AppState } from '@/app/state';
import {
  formatDashboardDate,
  reviewStatusTone,
  tenantDashboardPanel,
} from '@/app/tenantDashboardPanels';
import type {
  UITenantDashboardAudit,
  UITenantDashboardBuild,
  UITenantDashboardReview,
  UITenantDashboardUser,
} from '@/types';

import { InlineAlert } from './InlineAlert';
import { DashboardMessage, DataCell, DataTable } from './TenantDashboardMessage';

type TenantDashboardData = AppState['tenantDashboard']['data'];

export function TenantDashboardPanels({ data }: { data: TenantDashboardData }): React.ReactElement {
  return (
    <>
      <UsersPanel data={data} />
      <ReviewsPanel data={data} />
      <MergeQueuePanel data={data} />
      <BuildsPanel data={data} />
      <AuditPanel data={data} />
      <TabsContent value="api-log" className="min-h-0 overflow-auto">
        <APILogPanel log={data?.apiLog ?? ''} error={data?.apiLogError ?? ''} />
      </TabsContent>
    </>
  );
}

// ReviewsPanel is the review object's own home: status, branches, and — via
// each row — its builds, comment threads, and merge-queue position. The
// merge-queue tab stays a queue-shaped view of the same reviews.
function ReviewsPanel({ data }: { data: TenantDashboardData }): React.ReactElement {
  const dispatch = useAppDispatch();
  const reviews = data?.reviews ?? [];
  return (
    <TabsContent value="reviews" className="min-h-0 overflow-auto">
      <div className="mb-3 flex items-center justify-between gap-3">
        <span className="text-[13px] text-muted-foreground">
          {reviews.length} review{reviews.length === 1 ? '' : 's'}
        </span>
        <NewReviewAction data={data} />
      </div>
      <PanelBody
        data={data}
        tab="reviews"
        empty={
          <EmptyState
            heading="No reviews yet"
            body="A review appears here once someone opens one from the CLI's erun review create or the New review button above."
          />
        }
      >
        {reviews.length > 0 ? (
          <ReviewsTable
            reviews={reviews}
            onSelect={(review) => {
              void dispatch(openReviewDetail(review.reviewId));
            }}
          />
        ) : null}
      </PanelBody>
    </TabsContent>
  );
}

// NewReviewAction degrades by permission: the button renders only when the
// caller may create a review, and a caller who cannot is told so rather than
// left to discover it from a failed submit.
function NewReviewAction({ data }: { data: TenantDashboardData }): React.ReactElement | null {
  const dispatch = useAppDispatch();
  if (!data) {
    return null;
  }
  if (!data.canCreateReview) {
    return (
      <span className="text-[13px] text-muted-foreground">
        You do not have access to open reviews.
      </span>
    );
  }
  return (
    <Button
      type="button"
      size="sm"
      onClick={() => {
        void dispatch(openCreateReviewDialog());
      }}
    >
      <Plus aria-hidden="true" />
      New review
    </Button>
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
      <div className="mb-3 flex items-center justify-between gap-3">
        <span className="text-[13px] text-muted-foreground">{mergeQueue.length} queued</span>
        <AdvanceMergeQueueAction data={data} mergeQueue={mergeQueue} />
      </div>
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

// mergeQueueTargetBranch reports the target branch to advance only when the
// whole visible queue shares one — advancing is a single-queue-head write, so
// a mixed-branch queue has no single unambiguous head to name from here.
function mergeQueueTargetBranch(mergeQueue: UITenantDashboardReview[]): string {
  const branches = [...new Set(mergeQueue.map((review) => review.targetBranch.trim()))].filter(
    Boolean,
  );
  return branches.length === 1 ? (branches[0] ?? '') : '';
}

function AdvanceMergeQueueAction({
  data,
  mergeQueue,
}: {
  data: TenantDashboardData;
  mergeQueue: UITenantDashboardReview[];
}): React.ReactElement | null {
  const dispatch = useAppDispatch();
  const action = useAppSelector((state) => state.mergeQueueAction);
  if (!data) {
    return null;
  }
  if (!data.canAdvanceMergeQueue) {
    return (
      <span className="text-[13px] text-muted-foreground">
        You do not have access to advance the merge queue.
      </span>
    );
  }
  if (mergeQueue.length === 0) {
    return null;
  }
  const targetBranch = mergeQueueTargetBranch(mergeQueue);
  if (!targetBranch) {
    return (
      <span className="max-w-xs text-right text-[13px] text-muted-foreground">
        These reviews target more than one branch, so there is no single queue head to advance.
      </span>
    );
  }
  return (
    <div className="flex min-w-0 flex-col items-end gap-2">
      {action.confirming ? (
        <AdvanceMergeQueueConfirm targetBranch={targetBranch} busy={action.busy} />
      ) : (
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={() => {
            dispatch(confirmAdvanceMergeQueue());
          }}
        >
          Advance queue
        </Button>
      )}
      {action.error && (
        <div className="max-w-sm">
          <InlineAlert>{action.error}</InlineAlert>
        </div>
      )}
    </div>
  );
}

// The confirm step is its own row so the question reads left-to-right into the
// two answers, and a failure lands under it rather than shouldering its way
// into the middle of the sentence.
function AdvanceMergeQueueConfirm({
  targetBranch,
  busy,
}: {
  targetBranch: string;
  busy: boolean;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  return (
    <div className="flex flex-wrap items-center justify-end gap-2">
      <span className="text-[13px] text-foreground">
        Merge the queue head into <span className="font-mono">{targetBranch}</span>?
      </span>
      <Button
        type="button"
        variant="outline"
        size="sm"
        disabled={busy}
        onClick={() => {
          dispatch(cancelAdvanceMergeQueue());
        }}
      >
        Cancel
      </Button>
      <Button
        type="button"
        size="sm"
        disabled={busy}
        onClick={() => {
          void dispatch(submitAdvanceMergeQueue(targetBranch));
        }}
      >
        {busy && <LoaderCircle className="animate-spin" aria-hidden="true" />}
        Confirm
      </Button>
    </div>
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
  tab: 'users' | 'reviews' | 'queue' | 'builds' | 'audit';
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

function ReviewsTable({
  reviews,
  onSelect,
}: {
  reviews: UITenantDashboardReview[];
  onSelect?: (review: UITenantDashboardReview) => void;
}): React.ReactElement {
  return (
    <DataTable headers={['Review', 'Status', 'Target', 'Source', 'Updated']}>
      {reviews.map((review) => (
        <tr key={review.reviewId}>
          <DataCell strong>
            {onSelect ? (
              <button
                type="button"
                className="text-left text-foreground underline-offset-2 hover:underline focus-visible:underline"
                onClick={() => {
                  onSelect(review);
                }}
                aria-label={`Open review ${review.name || review.reviewId}`}
              >
                {review.name || review.reviewId}
              </button>
            ) : (
              review.name || review.reviewId
            )}
          </DataCell>
          <DataCell>
            <StatusBadge tone={reviewStatusTone(review.status)} label={review.status} />
          </DataCell>
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
