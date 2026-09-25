import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
  EmptyState,
  SelectField,
  StatusBadge,
  type StatusBadgeTone,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from 'erun-kit';
import { Activity } from 'lucide-react';
import * as React from 'react';

import type { Job, JobFilter, JobStatus } from '../app/api/jobsApi';
import type { JobsState } from './controller';
import { useJobsController } from './controller';

// JobsPanel answers, without the operator knowing any job ids: what is being
// worked on right now, by whom, and what recent work finished. It is the
// console's counterpart to `erun job list`.
//
// Two rules shape what this renders:
//
//   - The row shows the *summary* and never the command. A job's summary is
//     prose by contract (the API refuses one that is only a shell command),
//     and the pod's own job record keeps the command line as technical
//     detail. There is deliberately no field here that could render it.
//   - RUNNING is the queue; everything else is history. The live work leads,
//     grouped by job type, because that is the grouping an operator scans for
//     "is anyone gating?" or "is anyone fixing that issue?".
const STATUS_TONES: Record<JobStatus, StatusBadgeTone> = {
  // PLANNED is work recorded before it starts -- the rung above RUNNING, and
  // deliberately not toned as progress: nothing is running yet.
  PLANNED: 'muted',
  RUNNING: 'in-progress',
  SUCCEEDED: 'success',
  FAILED: 'destructive',
  // ABANDONED is a job whose actor stopped updating it, swept by the
  // platform. It is not a failure of the work and not a success -- `warning`
  // is the honest tone, the same distinction INCONCLUSIVE draws on the
  // gate-run queue.
  ABANDONED: 'warning',
  SUPERSEDED: 'muted',
  // A status this console does not know about is rendered as itself rather
  // than guessed at.
  UNKNOWN: 'muted',
};

const STATUS_FILTER_OPTIONS: { value: string; label: string }[] = [
  { value: '', label: 'All statuses' },
  { value: 'RUNNING', label: 'Running' },
  { value: 'SUCCEEDED', label: 'Succeeded' },
  { value: 'FAILED', label: 'Failed' },
  { value: 'ABANDONED', label: 'Abandoned' },
  { value: 'SUPERSEDED', label: 'Superseded' },
];

// JOB_TYPE_ORDER is the platform's own closed vocabulary, in the order the
// API documents it, so the grouping reads the same way `erun job list` does.
const JOB_TYPE_ORDER = [
  'fix',
  'review',
  'gate',
  'release',
  'deploy',
  'investigate',
  'plan',
  'triage',
  'maintenance',
];

// groupByJobType orders the groups by the vocabulary above, then by name for
// any type this console has not been taught yet -- appended rather than
// dropped, so a newer platform's job type shows up in a group of its own
// instead of vanishing from the queue.
function groupByJobType(jobs: Job[]): { jobType: string; jobs: Job[] }[] {
  const byType = new Map<string, Job[]>();
  for (const job of jobs) {
    const existing = byType.get(job.jobType);
    if (existing === undefined) {
      byType.set(job.jobType, [job]);
    } else {
      existing.push(job);
    }
  }
  const rank = (jobType: string): number => {
    const index = JOB_TYPE_ORDER.indexOf(jobType);
    return index === -1 ? JOB_TYPE_ORDER.length : index;
  };
  return [...byType.entries()]
    .map(([jobType, group]) => ({ jobType, jobs: group }))
    .sort((a, b) => rank(a.jobType) - rank(b.jobType) || a.jobType.localeCompare(b.jobType));
}

// formatElapsed is how long the job has been going: to its own end when it
// has one, to now while it is still running. Coarse on purpose -- an operator
// scanning a queue needs "about 20 minutes", not seconds precision.
function formatElapsed(startedAt: string, endedAt?: string): string {
  const start = Date.parse(startedAt);
  if (Number.isNaN(start)) {
    return '—';
  }
  const end = endedAt === undefined ? Date.now() : Date.parse(endedAt);
  if (Number.isNaN(end)) {
    return '—';
  }
  const seconds = Math.max(0, Math.round((end - start) / 1000));
  if (seconds < 60) {
    return `${String(seconds)}s`;
  }
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) {
    return seconds % 60 === 0
      ? `${String(minutes)}m`
      : `${String(minutes)}m ${String(seconds % 60)}s`;
  }
  const hours = Math.floor(minutes / 60);
  if (hours < 24) {
    return `${String(hours)}h ${String(minutes % 60)}m`;
  }
  return `${String(Math.floor(hours / 24))}d ${String(hours % 24)}h`;
}

// issueUrl turns a job's issue_ref in owner/repo#number form into a link, and
// returns undefined for anything else. An unparseable ref renders as plain
// text: a link built from a guess would send an operator somewhere that has
// nothing to do with the job.
function issueUrl(issueRef: string): string | undefined {
  const match = /^([^/\s#]+)\/([^/\s#]+)#(\d+)$/.exec(issueRef.trim());
  if (match === null) {
    return undefined;
  }
  const [, owner, repo, issueNumber] = match;
  if (owner === undefined || repo === undefined || issueNumber === undefined) {
    return undefined;
  }
  return `https://github.com/${owner}/${repo}/issues/${issueNumber}`;
}

function IssueRef({ issueRef }: { issueRef?: string }): React.ReactElement {
  if (issueRef === undefined || issueRef === '') {
    return <span className="text-muted-foreground">—</span>;
  }
  const href = issueUrl(issueRef);
  if (href === undefined) {
    return <span className="break-all text-xs">{issueRef}</span>;
  }
  return (
    <a className="break-all text-xs underline" href={href} target="_blank" rel="noreferrer">
      {issueRef}
    </a>
  );
}

function Actor({ job }: { job: Job }): React.ReactElement {
  return (
    <div className="grid gap-0.5">
      <span className="text-sm text-foreground">{job.actorId}</span>
      <span className="text-xs text-muted-foreground">{job.actorKind}</span>
    </div>
  );
}

function JobRow({ job, showStatus }: { job: Job; showStatus: boolean }): React.ReactElement {
  return (
    <TableRow>
      {showStatus && (
        <TableCell>
          <StatusBadge tone={STATUS_TONES[job.status]} label={job.status} />
        </TableCell>
      )}
      <TableCell className="max-w-md text-sm text-foreground">{job.summary}</TableCell>
      <TableCell>
        <IssueRef issueRef={job.issueRef} />
      </TableCell>
      <TableCell>
        <Actor job={job} />
      </TableCell>
      <TableCell className="text-sm text-muted-foreground">
        {formatElapsed(job.startedAt, job.endedAt)}
      </TableCell>
    </TableRow>
  );
}

function JobTable({ jobs, showStatus }: { jobs: Job[]; showStatus: boolean }): React.ReactElement {
  return (
    <Table>
      <TableHeader>
        <TableRow>
          {showStatus && <TableHead>Status</TableHead>}
          <TableHead>Summary</TableHead>
          <TableHead>Issue</TableHead>
          <TableHead>Actor</TableHead>
          <TableHead>Elapsed</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {jobs.map((job) => (
          <JobRow key={job.jobId} job={job} showStatus={showStatus} />
        ))}
      </TableBody>
    </Table>
  );
}

// RunningQueue is the live half: RUNNING jobs, grouped by job type, oldest
// claim first within each group -- the one that has been held longest is the
// one most likely to be stuck.
function RunningQueue({ jobs }: { jobs: Job[] }): React.ReactElement {
  const groups = groupByJobType(jobs);
  if (groups.length === 0) {
    return (
      <p className="text-sm text-muted-foreground">
        Nothing is running right now. A job appears here the moment an agent or orchestrator records
        what it is working on.
      </p>
    );
  }
  return (
    <div className="grid gap-4">
      {groups.map((group) => (
        <div key={group.jobType} className="grid gap-2">
          <h3 className="text-sm font-medium text-foreground">
            {group.jobType} <span className="text-muted-foreground">({group.jobs.length})</span>
          </h3>
          <JobTable jobs={group.jobs} showStatus={false} />
        </div>
      ))}
    </div>
  );
}

function FinishedJobs({ jobs }: { jobs: Job[] }): React.ReactElement | null {
  if (jobs.length === 0) {
    return null;
  }
  return (
    <div className="grid gap-2">
      <h3 className="text-sm font-medium text-foreground">Recently finished</h3>
      <JobTable jobs={jobs} showStatus />
    </div>
  );
}

function JobsBody({ state }: { state: JobsState }): React.ReactElement {
  if (state.status === 'loading') {
    return (
      <p className="text-sm text-muted-foreground" role="status">
        Loading jobs…
      </p>
    );
  }
  if (state.status === 'error') {
    return (
      <p className="text-sm text-destructive" role="alert">
        Could not load jobs: {state.message}
      </p>
    );
  }
  if (state.jobs.length === 0) {
    return (
      <EmptyState
        icon={<Activity />}
        heading="No jobs match this filter."
        body="A job appears here the moment an agent or orchestrator records what it is working on."
      />
    );
  }
  const running = state.jobs.filter((job) => job.status === 'RUNNING');
  const finished = state.jobs.filter((job) => job.status !== 'RUNNING');
  return (
    <div className="grid gap-6">
      <RunningQueue jobs={running} />
      <FinishedJobs jobs={finished} />
    </div>
  );
}

// JobsPanel is the tenant's own view of what is being worked on: the live
// queue first, grouped by job type, then what recently finished. Read-only --
// the actor doing the work records it.
export function JobsPanel({ token }: { token: string }): React.ReactElement {
  const [status, setStatus] = React.useState('');
  const filter: JobFilter = status === '' ? {} : { status: status as JobStatus };
  const state = useJobsController(token, filter);

  return (
    <Card aria-labelledby="jobs-heading">
      <CardHeader>
        <CardTitle id="jobs-heading">
          <Activity className="mr-2 inline size-4" aria-hidden="true" />
          Jobs
        </CardTitle>
      </CardHeader>
      <CardContent className="grid gap-4">
        <div className="max-w-xs">
          <SelectField
            id="job-status-filter"
            label="Status"
            value={status}
            options={STATUS_FILTER_OPTIONS}
            onChange={setStatus}
          />
        </div>
        <JobsBody state={state} />
      </CardContent>
    </Card>
  );
}
