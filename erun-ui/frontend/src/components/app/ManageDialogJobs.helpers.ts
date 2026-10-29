// The generated Wails model types exitCode as number, but the wire sends null for jobs without an outcome.
export interface JobView {
  id: string;
  name: string;
  state: string;
  kind?: string;
  agentTool?: string;
  command?: string[];
  dir?: string;
  exitCode: number | null;
  startedAtUnix?: number;
  endedAtUnix?: number;
  progress?: { activity?: string; lastMessage?: string; turns: number; toolsRun: number };
}

// A job gone without an outcome is neither success nor failure.
export type JobOutcome =
  | 'running'
  | 'succeeded'
  | 'failed'
  | 'signalled'
  | 'abandoned'
  | 'gate-incomplete'
  | 'unknown';

export function jobOutcome(job: JobView): JobOutcome {
  if (job.state === 'running') {
    return 'running';
  }
  if (job.state === 'unknown') {
    return 'unknown';
  }
  // Abandoned and gate-incomplete are terminal but not verdicts; their clean exit code does not speak for the work.
  if (job.state === 'abandoned') {
    return 'abandoned';
  }
  if (job.state === 'gate-incomplete') {
    return 'gate-incomplete';
  }
  if (job.exitCode === null) {
    return 'unknown';
  }
  if (job.exitCode === 0) {
    return 'succeeded';
  }
  // -1 means terminated by a signal, which is what a cancel produces.
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
    case 'abandoned':
      return 'Abandoned (work still running)';
    case 'gate-incomplete':
      return 'Gate incomplete (no verdict)';
    case 'failed':
      return `Failed (exit ${String(job.exitCode)})`;
    default:
      return 'Outcome unknown';
  }
}

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

function agentToolLabel(tool: string | undefined): string {
  if (tool === 'claude') {
    return 'Claude';
  }
  if (tool === 'codex') {
    return 'Codex';
  }
  return 'Agent';
}

// An agent job's argv carries the whole prompt, so show its activity instead.
function agentJobDetailLine(job: JobView): string {
  if (job.progress?.activity) {
    return job.progress.activity;
  }
  if (job.progress?.lastMessage) {
    return job.progress.lastMessage;
  }
  return `${agentToolLabel(job.agentTool)} agent`;
}

export function jobDetailLine(job: JobView): string {
  if (job.kind === 'agent') {
    return agentJobDetailLine(job);
  }
  if (job.progress?.activity) {
    return job.progress.activity;
  }
  if (job.command && job.command.length > 0) {
    return job.command.join(' ');
  }
  return job.dir ?? '';
}
