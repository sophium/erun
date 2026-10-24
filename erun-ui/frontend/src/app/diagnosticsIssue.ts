import type { UIBuildDetails } from '@/types';

import type { DiagnosticsReportContext } from './diagnosticsReport';

// Prefills a GitHub issue the operator submits themselves; the desktop never
// files one silently.
const ERUN_ISSUE_URL = 'https://github.com/sophium/erun/issues/new';

// Stays under the URL length GitHub and browsers accept.
export const DIAGNOSTICS_ISSUE_URL_MAX_LENGTH = 8000;

const TRUNCATION_NOTICE = '\n\n… truncated — the full report is on your clipboard.';

export function diagnosticsIssueTitle(context: DiagnosticsReportContext): string {
  switch (context.kind) {
    case 'environment': {
      const target = `${context.tenant}/${context.environment}`;
      return context.status ? `${target}: ${context.status}` : `${target}: diagnostics`;
    }
    case 'orchestrator': {
      const { orchestrator } = context;
      return orchestrator.status === 'running'
        ? `Orchestrator ${orchestrator.name}: diagnostics`
        : `Orchestrator ${orchestrator.name}: ${orchestrator.status}`;
    }
    case 'app':
      return 'ERun desktop: diagnostics';
  }
}

function buildLine(build: UIBuildDetails | null): string {
  if (!build) {
    return '- erun version: unknown';
  }
  const extras = [build.commit, build.date].filter(Boolean).join(' ');
  return `- erun version: ${build.version}${extras ? ` (${extras})` : ''}`;
}

// diagnosticsIssueBody follows the erun-file-issue skill's template.
export function diagnosticsIssueBody(report: string, build: UIBuildDetails | null): string {
  return [
    '## What happened',
    '',
    '<describe what you observed>',
    '',
    '## What you expected',
    '',
    '<describe what you expected instead>',
    '',
    '## Reproduction',
    '',
    '<steps to reproduce>',
    '',
    '## Environment',
    '',
    buildLine(build),
    '- context: desktop app',
    '',
    '## Diagnostics evidence',
    '',
    '```',
    report.trimEnd(),
    '```',
  ].join('\n');
}

function encodeIssueURL(title: string, body: string): string {
  return `${ERUN_ISSUE_URL}?title=${encodeURIComponent(title)}&body=${encodeURIComponent(body)}&labels=bug`;
}

export interface DiagnosticsIssuePrefill {
  url: string;
  truncated: boolean;
}

// buildDiagnosticsIssueURL trims only the body to fit, noting that the full
// report is on the clipboard.
export function buildDiagnosticsIssueURL(
  title: string,
  body: string,
  maxLength: number = DIAGNOSTICS_ISSUE_URL_MAX_LENGTH,
): DiagnosticsIssuePrefill {
  const full = encodeIssueURL(title, body);
  if (full.length <= maxLength) {
    return { url: full, truncated: false };
  }
  let lo = 0;
  let hi = body.length;
  while (lo < hi) {
    const mid = Math.ceil((lo + hi) / 2);
    const candidate = encodeIssueURL(title, `${body.slice(0, mid)}${TRUNCATION_NOTICE}`);
    if (candidate.length <= maxLength) {
      lo = mid;
    } else {
      hi = mid - 1;
    }
  }
  return {
    url: encodeIssueURL(title, `${body.slice(0, lo)}${TRUNCATION_NOTICE}`),
    truncated: true,
  };
}
