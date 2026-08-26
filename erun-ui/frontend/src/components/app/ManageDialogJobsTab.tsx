import { Button } from 'erun-kit';
import { AlertTriangle, Ban, CheckCircle2, ListChecks, LoaderCircle, XCircle } from 'lucide-react';
import * as React from 'react';

import { readError } from '@/app/errors';
import { InlineAlert } from '@/components/app/InlineAlert';
import { JobCancelAction } from '@/components/app/ManageDialogJobCancel';
import { JobOutputView } from '@/components/app/ManageDialogJobOutput';
import type { JobView } from '@/components/app/ManageDialogJobs.helpers';
import {
  jobDetailLine,
  jobDurationLabel,
  jobOutcome,
  jobOutcomeLabel,
} from '@/components/app/ManageDialogJobs.helpers';
import type { UISelection } from '@/types';

import { LoadEnvironmentJobs } from '../../../wailsjs/go/main/App';

// The desktop already writes into the job store -- Investigate starts a job
// there -- and could not show the job it had just created. This is the missing
// lifecycle half: what ran, how it ended, what it printed, and a way to stop it.
export function JobsTab({
  selection,
  open,
}: {
  selection: UISelection | null;
  open: boolean;
}): React.ReactElement {
  const [jobs, setJobs] = React.useState<JobView[]>([]);
  const [loading, setLoading] = React.useState(false);
  const [error, setError] = React.useState('');
  const [nowUnix, setNowUnix] = React.useState(() => Math.floor(Date.now() / 1000));

  const reload = React.useCallback(() => {
    if (!selection) {
      return;
    }
    setLoading(true);
    setError('');
    LoadEnvironmentJobs(selection.tenant, selection.environment)
      .then((next) => {
        // The generated model claims a non-null exitCode; the wire sends null.
        setJobs(next as unknown as JobView[]);
      })
      .catch((err: unknown) => {
        setError(readError(err));
      })
      .finally(() => {
        setLoading(false);
      });
  }, [selection]);

  React.useEffect(() => {
    if (!open) {
      return;
    }
    reload();
  }, [open, reload]);

  // A running job's duration would otherwise freeze at whatever it was when the
  // tab opened. One tick a second, and only while the tab is open and something
  // is actually running, so nothing animates on a closed dialog.
  const hasRunning = jobs.some((job) => job.state === 'running');
  React.useEffect(() => {
    if (!open || !hasRunning) {
      return;
    }
    const timer = setInterval(() => {
      setNowUnix(Math.floor(Date.now() / 1000));
    }, 1000);
    return () => {
      clearInterval(timer);
    };
  }, [open, hasRunning]);

  if (!selection) {
    return <JobsEmptyState message="Select an environment to see its jobs." />;
  }
  if (loading && jobs.length === 0) {
    return <JobsEmptyState message="Loading jobs…" />;
  }
  if (error) {
    return <InlineAlert>Could not read this environment&apos;s jobs. {error}</InlineAlert>;
  }
  if (jobs.length === 0) {
    return (
      <JobsEmptyState message="No jobs yet. Detached work — a build, a test run, an agent — is recorded here with its outcome and output, and stays readable for 24 hours after it ends." />
    );
  }
  return (
    <div className="flex flex-col gap-2" data-testid="manage-jobs-list">
      <p className="text-[13px] text-muted-foreground">
        {jobs.length === 1 ? '1 job' : `${String(jobs.length)} jobs`}, newest first.
      </p>
      {jobs.map((job) => (
        <JobRow key={job.id} job={job} selection={selection} nowUnix={nowUnix} onChanged={reload} />
      ))}
    </div>
  );
}

function JobsEmptyState({ message }: { message: string }): React.ReactElement {
  return (
    <div
      className="flex items-center gap-3 rounded-[var(--radius)] border border-border bg-muted/40 px-3 py-3 text-[13px] text-muted-foreground"
      data-testid="manage-jobs-empty"
    >
      <ListChecks className="size-4 shrink-0" aria-hidden="true" />
      <span>{message}</span>
    </div>
  );
}

// Colour never carries the outcome on its own: each state has its own icon and
// its own words, so the row reads the same to someone who cannot separate the
// green from the red.
function OutcomeBadge({ job }: { job: JobView }): React.ReactElement {
  const outcome = jobOutcome(job);
  const label = jobOutcomeLabel(job);
  const shared = 'inline-flex items-center gap-1.5 text-[12px] font-medium';
  if (outcome === 'running') {
    return (
      <span className={`${shared} text-foreground`} data-testid="manage-jobs-row-outcome">
        <LoaderCircle className="size-3.5 animate-spin" aria-hidden="true" />
        {label}
      </span>
    );
  }
  if (outcome === 'succeeded') {
    return (
      <span
        className={`${shared} text-green-700 dark:text-green-400`}
        data-testid="manage-jobs-row-outcome"
      >
        <CheckCircle2 className="size-3.5" aria-hidden="true" />
        {label}
      </span>
    );
  }
  if (outcome === 'signalled') {
    return (
      <span className={`${shared} text-muted-foreground`} data-testid="manage-jobs-row-outcome">
        <Ban className="size-3.5" aria-hidden="true" />
        {label}
      </span>
    );
  }
  if (outcome === 'unknown') {
    return (
      <span
        className={`${shared} text-amber-600 dark:text-amber-400`}
        data-testid="manage-jobs-row-outcome"
      >
        <AlertTriangle className="size-3.5" aria-hidden="true" />
        {label}
      </span>
    );
  }
  return (
    <span className={`${shared} text-destructive`} data-testid="manage-jobs-row-outcome">
      <XCircle className="size-3.5" aria-hidden="true" />
      {label}
    </span>
  );
}

function JobRow({
  job,
  selection,
  nowUnix,
  onChanged,
}: {
  job: JobView;
  selection: UISelection;
  nowUnix: number;
  onChanged: () => void;
}): React.ReactElement {
  const [showOutput, setShowOutput] = React.useState(false);
  const duration = jobDurationLabel(job, nowUnix);
  const detail = jobDetailLine(job);
  const label = job.name || job.id;

  return (
    <article
      className="grid gap-1.5 rounded-[var(--radius)] border border-border bg-background px-3 py-2.5"
      data-testid="manage-jobs-row"
    >
      <header className="flex items-baseline justify-between gap-3">
        <span
          className="min-w-0 truncate text-[13px] font-medium text-foreground"
          data-testid="manage-jobs-row-name"
        >
          {label}
        </span>
        <OutcomeBadge job={job} />
      </header>
      {detail && (
        <div
          className="text-[12px] leading-[1.4] text-muted-foreground [overflow-wrap:anywhere]"
          data-testid="manage-jobs-row-detail"
        >
          {detail}
        </div>
      )}
      <div className="flex items-center justify-between gap-3">
        <span className="text-[12px] text-muted-foreground" data-testid="manage-jobs-row-duration">
          {jobTimingLabel(job.state, duration)}
        </span>
        <div className="flex items-center gap-1.5">
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={() => {
              setShowOutput((shown) => !shown);
            }}
            aria-expanded={showOutput}
            aria-label={`${showOutput ? 'Hide' : 'Show'} output for ${label}`}
          >
            {showOutput ? 'Hide output' : 'Show output'}
          </Button>
          <JobCancelAction job={job} selection={selection} onCancelled={onChanged} />
        </div>
      </div>
      {showOutput && <JobOutputView job={job} selection={selection} />}
    </article>
  );
}

// Running work reads as elapsed; finished work reads as how long it took.
function jobTimingLabel(state: string, duration: string): string {
  if (!duration) {
    return '';
  }
  return state === 'running' ? `Running for ${duration}` : `Took ${duration}`;
}
