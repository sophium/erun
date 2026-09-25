import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
  EmptyState,
  PIPELINE_LADDER,
  rungLabel,
  rungTone,
  StatusBadge,
} from 'erun-kit';
import { GitBranch, ListTree } from 'lucide-react';
import * as React from 'react';

import type { PipelineIssue, PipelineItem } from '../app/api/pipelineApi';
import type { PipelineState } from './controller';
import { usePipelineController } from './controller';

// PipelinePanel answers "what is going on?" in one read: planned work, work in
// progress, reviews open, ready to merge, merging, merged -- across the jobs
// that record planned and in-flight work and the reviews moving through the
// merge queue, unioned on the issue each belongs to.
//
// Three rules shape what this renders:
//
//   - The rung is the platform's, not the console's. A rung this client does
//     not recognise is rendered under its own spelling and toned as an
//     outcome, never folded into a rung it resembles; the platform being ahead
//     of the console is not a licence to describe work the console cannot see.
//   - A link parsed out of a branch name is a guess about the branch, and it
//     says so. `issueRefSource` of INFERRED renders with an explicit marker,
//     because presenting a guess as something the author declared is the one
//     claim this provenance exists to refuse.
//   - A row says which record it came from. A job has no branch; a review has
//     no scope. A row that could not tell them apart would send an operator
//     looking for one that does not exist.

// issueUrl turns a canonical owner/repo#number key into a link, and returns
// undefined for anything else. An unparseable key renders as plain text: a
// link built from a guess sends an operator somewhere that has nothing to do
// with the work.
function issueUrl(issueKey: string): string | undefined {
  const match = /^([^/\s#]+)\/([^/\s#]+)#(\d+)$/.exec(issueKey.trim());
  if (match === null) {
    return undefined;
  }
  const [, owner, repo, issueNumber] = match;
  if (owner === undefined || repo === undefined || issueNumber === undefined) {
    return undefined;
  }
  return `https://github.com/${owner}/${repo}/issues/${issueNumber}`;
}

function IssueLink({ issueKey }: { issueKey: string }): React.ReactElement {
  if (issueKey === '') {
    return <span className="text-xs text-muted-foreground">No issue</span>;
  }
  const href = issueUrl(issueKey);
  if (href === undefined) {
    return <span className="break-all text-xs">{issueKey}</span>;
  }
  return (
    <a className="break-all text-xs underline" href={href} target="_blank" rel="noreferrer">
      {issueKey}
    </a>
  );
}

// RefSource marks a link that was parsed out of a branch name rather than
// stated. It is the whole reason the field travels.
function RefSource({ item }: { item: PipelineItem }): React.ReactElement | null {
  if (item.issueRefSource !== 'INFERRED') {
    return null;
  }
  return (
    <span
      className="text-xs text-muted-foreground"
      title="Inferred from the source branch name; the author did not record this issue"
    >
      inferred from branch
    </span>
  );
}

function ItemSubject({ item }: { item: PipelineItem }): React.ReactElement {
  if (item.job !== undefined) {
    const { job } = item;
    return (
      <div className="grid gap-0.5">
        <span className="text-sm text-foreground">{job.summary}</span>
        <span className="text-xs text-muted-foreground">
          job · {job.jobType} · {job.actorId}
        </span>
      </div>
    );
  }
  const review = item.review;
  if (review === undefined) {
    return <span className="text-sm text-muted-foreground">—</span>;
  }
  return (
    <div className="grid gap-0.5">
      <span className="text-sm text-foreground">{review.name}</span>
      <span className="flex items-center gap-1 text-xs text-muted-foreground">
        <GitBranch className="size-3" aria-hidden="true" />
        review · {review.sourceBranch} → {review.targetBranch}
      </span>
    </div>
  );
}

function ItemRow({ item }: { item: PipelineItem }): React.ReactElement {
  return (
    <li className="flex items-start gap-3 border-t border-border px-3 py-2 first:border-t-0">
      <StatusBadge tone={rungTone(item.rung)} label={rungLabel(item.rung)} />
      <div className="grid min-w-0 gap-0.5">
        <ItemSubject item={item} />
        {item.issueRef !== undefined && item.issueRef !== '' && (
          <span className="flex items-center gap-2 break-all text-xs text-muted-foreground">
            {item.issueRef}
            <RefSource item={item} />
          </span>
        )}
      </div>
    </li>
  );
}

function IssueGroup({ issue }: { issue: PipelineIssue }): React.ReactElement {
  return (
    <div className="rounded-md border border-border">
      <div className="flex items-center gap-2 bg-muted/40 px-3 py-2">
        <IssueLink issueKey={issue.issueKey} />
      </div>
      <ul className="grid">
        {issue.items.map((item) => (
          <ItemRow key={item.job?.jobId ?? item.review?.reviewId ?? 'item'} item={item} />
        ))}
      </ul>
    </div>
  );
}

function PipelineBody({ state }: { state: PipelineState }): React.ReactElement {
  if (state.status === 'loading') {
    return (
      <p className="text-sm text-muted-foreground" role="status">
        Loading the pipeline…
      </p>
    );
  }
  if (state.status === 'error') {
    return (
      <p className="text-sm text-destructive" role="alert">
        Could not load the pipeline: {state.message}
      </p>
    );
  }
  if (state.issues.length === 0) {
    return (
      <EmptyState
        icon={<ListTree />}
        heading="Nothing is in the pipeline."
        body="Work appears here the moment an agent records a planned or running job, or a review is opened."
      />
    );
  }
  return (
    <div className="grid gap-4">
      {state.issues.map((issue) => (
        <IssueGroup key={issue.issueKey === '' ? 'unlinked' : issue.issueKey} issue={issue} />
      ))}
    </div>
  );
}

// PipelinePanel is the tenant's own view of the whole pipeline. Read-only: the
// actors doing the work record it, and the merge queue moves reviews.
export function PipelinePanel({ token }: { token: string }): React.ReactElement {
  const state = usePipelineController(token);

  return (
    <Card aria-labelledby="pipeline-heading">
      <CardHeader>
        <CardTitle id="pipeline-heading">
          <ListTree className="mr-2 inline size-4" aria-hidden="true" />
          Pipeline
        </CardTitle>
      </CardHeader>
      <CardContent className="grid gap-4">
        <p className="text-sm text-muted-foreground">
          Planned work and the review lifecycle, unioned on the issue each belongs to:{' '}
          {PIPELINE_LADDER.map((rung) => rungLabel(rung)).join(' → ')}.
        </p>
        <PipelineBody state={state} />
      </CardContent>
    </Card>
  );
}
