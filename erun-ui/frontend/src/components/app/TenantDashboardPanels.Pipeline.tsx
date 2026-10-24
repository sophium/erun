import { EmptyState, rungLabel, rungTone, StatusBadge, TabsContent } from 'erun-kit';
import { ListTree } from 'lucide-react';
import * as React from 'react';

import type { UIPipelineIssue, UIPipelineItem } from '@/types';

import {
  BranchArrow,
  DataCell,
  DataTable,
  PanelBody,
  type TenantDashboardData,
} from './TenantDashboardMessage';

// PipelinePanel renders GET /v1/pipeline read-only, like the console's
// Pipeline section. Rungs come from erun-kit's pipelineRungs (unknown rungs keep
// their spelling), and links inferred from branch names are marked as such.
export function PipelinePanel({ data }: { data: TenantDashboardData }): React.ReactElement {
  const issues = data?.pipeline ?? [];
  return (
    <TabsContent value="pipeline" className="min-h-0 overflow-auto">
      <PanelBody
        data={data}
        tab="pipeline"
        empty={
          <EmptyState
            icon={<ListTree />}
            heading="Nothing is in the pipeline"
            body="Work appears here the moment an agent records a planned or running job, or a review is opened."
          />
        }
      >
        {issues.length > 0 ? <PipelineTable issues={issues} /> : null}
      </PanelBody>
    </TabsContent>
  );
}

// Rows keep the platform's issue grouping and rung order.
interface PipelineRow {
  issue: UIPipelineIssue;
  item: UIPipelineItem;
}

function pipelineRows(issues: UIPipelineIssue[]): PipelineRow[] {
  return issues.flatMap((issue) => issue.items.map((item) => ({ issue, item })));
}

function rowKey({ item }: PipelineRow): string {
  return item.job?.jobId ?? item.review?.reviewId ?? `${item.rung}:${item.issueKey}`;
}

function PipelineTable({ issues }: { issues: UIPipelineIssue[] }): React.ReactElement {
  return (
    <DataTable
      headers={['Issue', 'Rung', 'Work']}
      columnWidths={['w-[26%]', 'w-[130px]', '']}
      minWidthClassName="min-w-[720px]"
    >
      {pipelineRows(issues).map((row) => (
        <PipelineRowCells key={rowKey(row)} issue={row.issue} item={row.item} />
      ))}
    </DataTable>
  );
}

function PipelineRowCells({
  issue,
  item,
}: {
  issue: UIPipelineIssue;
  item: UIPipelineItem;
}): React.ReactElement {
  return (
    <tr>
      <DataCell>
        <IssueCell issueKey={issue.issueKey} item={item} />
      </DataCell>
      <DataCell>
        <StatusBadge tone={rungTone(item.rung)} label={rungLabel(item.rung)} />
      </DataCell>
      <DataCell>
        <WorkCell item={item} />
      </DataCell>
    </tr>
  );
}

// Plain text: the desktop has no verified way to open an external issue.
function IssueCell({
  issueKey,
  item,
}: {
  issueKey: string;
  item: UIPipelineItem;
}): React.ReactElement {
  if (issueKey.trim() === '') {
    return <span className="text-muted-foreground">No issue</span>;
  }
  return (
    <div className="grid gap-0.5">
      <span className="break-all font-mono text-[12px]">{issueKey}</span>
      {item.issueRefSource === 'INFERRED' && (
        <span
          className="text-xs text-muted-foreground"
          title="Inferred from the source branch name; the author did not record this issue"
        >
          inferred from branch
        </span>
      )}
    </div>
  );
}

function WorkCell({ item }: { item: UIPipelineItem }): React.ReactElement {
  const job = item.job;
  if (job !== undefined) {
    return (
      <div className="grid gap-0.5">
        <span className="truncate" title={job.summary}>
          {job.summary}
        </span>
        <span className="truncate text-xs text-muted-foreground">
          job · {job.jobType} · {job.actorId}
        </span>
      </div>
    );
  }
  const review = item.review;
  if (review === undefined) {
    // The platform sends exactly one of the two; render the row rather than drop it.
    return <span className="text-muted-foreground">—</span>;
  }
  return (
    <div className="grid gap-0.5">
      <span className="truncate" title={review.name}>
        {review.name}
      </span>
      <span className="flex min-w-0 items-center gap-1 text-xs text-muted-foreground">
        <span className="flex-none">review ·</span>
        <BranchArrow source={review.sourceBranch} target={review.targetBranch} />
      </span>
    </div>
  );
}
