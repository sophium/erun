// The generated Wails model types exitCode as a plain number because Go's
// *int maps to one, but the wire value is null for every job without an
// outcome. This is the shape the helpers actually receive.
export interface JobView {
  id: string;
  name: string;
  state: string;
  command?: string[];
  dir?: string;
  exitCode: number | null;
  startedAtUnix?: number;
  endedAtUnix?: number;
  progress?: { activity?: string };
}

// How a job's outcome reads to an operator. A job that is gone without an
// outcome is its own case: reporting it as success would be a lie, and
// reporting it as failure would blame work that may well have finished.
export type JobOutcome = 'running' | 'succeeded' | 'failed' | 'signalled' | 'unknown';

export function jobOutcome(job: JobView): JobOutcome {
  if (job.state === 'running') {
    return 'running';
  }
  if (job.state === 'unknown') {
    return 'unknown';
  }
  if (job.exitCode === null) {
    return 'unknown';
  }
  if (job.exitCode === 0) {
    return 'succeeded';
  }
  // The supervisor records -1 when the work was terminated by a signal, which
  // is what a cancel produces -- distinct from work that ran and failed.
  return job.exitCode === -1 ? 'signalled' : 'failed';
}

export function jobOutcomeLabel(job: JobView): string {
  switch (jobOutcome(job)) {
    case 'running':
      return 'Running';
    case 'succeeded':
      return 'Succeeded';
    case 'signalled':
      return 'Cancelled';
    case 'failed':
      return `Failed (exit ${String(job.exitCode)})`;
    default:
      return 'Outcome unknown';
  }
}

// A duration, not a formatted instant: what the operator wants to know about a
// job is how long it has been going or how long it took.
export function jobDurationLabel(job: JobView, nowUnix: number): string {
  if (!job.startedAtUnix) {
    return '';
  }
  const end = job.endedAtUnix ?? 0;
  const seconds = Math.max(0, (end > 0 ? end : nowUnix) - job.startedAtUnix);
  return formatDuration(seconds);
}

export function formatDuration(seconds: number): string {
  if (seconds < 60) {
    return `${String(seconds)}s`;
  }
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) {
    const rest = seconds % 60;
    return rest === 0 ? `${String(minutes)}m` : `${String(minutes)}m${String(rest)}s`;
  }
  const hours = Math.floor(minutes / 60);
  const restMinutes = minutes % 60;
  return restMinutes === 0 ? `${String(hours)}h` : `${String(hours)}h${String(restMinutes)}m`;
}

// What the row shows as the job's own description. An agent job's current
// activity is more useful than its argv, which is always the same tool.
export function jobDetailLine(job: JobView): string {
  if (job.progress?.activity) {
    return job.progress.activity;
  }
  if (job.command && job.command.length > 0) {
    return job.command.join(' ');
  }
  return job.dir ?? '';
}
