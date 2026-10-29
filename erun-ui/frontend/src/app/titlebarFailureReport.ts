// The "Target:"/"Message:"/"Output:" labels are what investigation_bounds.go
// requires of an actionable report.
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
