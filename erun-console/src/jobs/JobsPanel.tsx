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

// A row shows the job's prose summary, never its command line.
const STATUS_TONES: Record<JobStatus, StatusBadgeTone> = {
  // Not toned as progress: nothing is running yet.
  PLANNED: 'muted',
  RUNNING: 'in-progress',
  SUCCEEDED: 'success',
  FAILED: 'destructive',
  // Neither failure nor success.
  ABANDONED: 'warning',
  SUPERSEDED: 'muted',
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

// The platform's job type vocabulary, in documented order.
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

// Unknown types are appended, not dropped, so a newer platform's types still show.
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

// Coarse on purpose: a queue needs "about 20 minutes", not seconds.
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

// Undefined for an unparseable ref: a guessed link would mislead.
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

// Oldest claim first: the longest-held is the most likely stuck.
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
