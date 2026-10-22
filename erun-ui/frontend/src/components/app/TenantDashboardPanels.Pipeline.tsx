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

// PipelinePanel answers "what is going on?" in one read: planned work, work in
// progress, reviews open, ready to merge, merging, merged -- across the jobs
// that record planned and in-flight work and the reviews moving through the
// merge queue, unioned on the issue each belongs to. It is the desktop
// counterpart to the console's Pipeline section and reads the same
// GET /v1/pipeline.
//
// Three rules shape what this renders, and the console's PipelinePanel holds
// the same three:
//
//   - The rung is the platform's, not this panel's. The name and tone come
//     from erun-kit's shared pipelineRungs, so a rung this build has never
//     heard of renders under its own spelling and is toned as an outcome
//     rather than folded into a rung it resembles.
//   - A link parsed out of a branch name is a guess about the branch, and it
//     says so. issueRefSource of INFERRED renders with an explicit marker,
//     because presenting a guess as something the author declared is the one
//     claim this provenance exists to refuse.
//   - A row says which record it came from. A job has no branch; a review has
//     no scope. A row that could not tell them apart would send an operator
//     looking for one that does not exist.
//
// The panel is read-only. The actors doing the work record it, and the merge
// queue moves reviews; nothing here advances anything.
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

// pipelineRows flattens the platform's grouping into the table's rows without
// reordering anything. The platform groups by issue and orders items by rung,
// and the render keeps that order: the Issue cell then says, on every row,
// what the grouping was, so a table can carry the union without a second
// layout idiom inventing its own idea of which rows belong together.
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

// IssueCell renders the canonical owner/repo#number the work is filed under,
// as text: the desktop has no verified treatment for opening an external
// issue in the operator's browser, and a link that went nowhere would be a
// worse answer than the identifier itself, which is copyable as it stands.
//
// It is the item's resolved link rather than the item's raw issueRef, because
// the raw one is exactly what the canonical spelling was joined from -- for a
// branch-derived link the two differ only in the repository the review
// recorded, and showing both would state one link twice.
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

// WorkCell names the work and the record it came from. The second line is the
// console's own subject line, kept the same in both surfaces: a job states
// what kind of work it is and who holds it, a review states the branches it
// moves between.
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
    // The platform sends exactly one of the two. Rendering the row anyway,
    // with no subject, says "this record arrived without a body" instead of
    // dropping work the operator would then never know was there.
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
