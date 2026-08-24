import type { UIBuildDetails } from '@/types';

import type { DiagnosticsReportContext } from './diagnosticsReport';

// diagnosticsIssue turns the Diagnostics console's report into a prefilled
// GitHub issue the operator reviews and edits before submitting — filing
// itself stays a Submit-button action on github.com, never a silent `gh`
// call from the desktop (no credentials, no confirmation step).
const ERUN_ISSUE_URL = 'https://github.com/sophium/erun/issues/new';

// GitHub and browsers cap a request URL in the single-digit-KB range; this
// stays comfortably under that so the prefilled link always opens.
export const DIAGNOSTICS_ISSUE_URL_MAX_LENGTH = 8000;

const TRUNCATION_NOTICE = '\n\n… truncated — the full report is on your clipboard.';

// diagnosticsIssueTitle prefills from the context's own identity rather than
// leaving the title blank: an untitled issue is worse than no button.
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

// diagnosticsIssueBody mirrors the erun-file-issue skill's own template (What
// happened / What you expected / Reproduction / Environment) so a
// desktop-filed issue reads like one a human filed by hand, with the
// evidence the operator would otherwise have had to copy in themselves
// attached as its own section rather than dumped in place of the narrative.
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

// buildDiagnosticsIssueURL trims the body — never the title — down to the
// longest prefix whose encoded URL still fits the cap, appending a notice
// that the untrimmed report is on the clipboard so nothing is silently lost.
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
