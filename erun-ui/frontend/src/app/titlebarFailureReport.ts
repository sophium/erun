// titlebarFailureReport turns a titlebar error status into the same
// paste-ready, labelled report shape ActivityQueueDrawer.helpers.ts'
// buildFailureReport produces for a failed activity entry — the desktop's
// investigation_bounds.go floor looks for exactly these labels ("Target:",
// "Message:", "Output:") to decide a report carries something to act on, so a
// titlebar status (which has no ActivityQueueEntry behind it) needs its own
// builder that still passes that floor for a genuine error.

export interface TitlebarFailureReportSource {
  message: string;
  detail: string;
  copyOutput: string;
  envTenant?: string;
  envEnvironment?: string;
}

export function buildTitlebarFailureReport(status: TitlebarFailureReportSource): string {
  const lines = ['erun desktop status: error'];
  if (status.envTenant || status.envEnvironment) {
    lines.push(`Target: ${status.envTenant ?? 'unknown'}/${status.envEnvironment ?? 'unknown'}`);
  }
  lines.push(`Message: ${status.message}`);
  if (status.detail) {
    lines.push(`Detail: ${status.detail}`);
  }
  if (status.copyOutput && status.copyOutput !== status.message) {
    lines.push('', 'Output:', status.copyOutput);
  }
  return lines.join('\n');
}
