import type { UIBuildDetails, UIEnvironment, UIEnvTrace, UISelection } from '@/types';

import { formatUITrace, type UITraceEntry } from './uiTraceBuffer';

// formatDiagnosticsReport renders the Diagnostics console's one-click bug
// report (issue #514): everything a reader needs in a single paste-ready
// block — app build, env identity and state, the erun trace tail (or its
// empty-state reason: "unreachable" is itself evidence), and the UI action
// history. Mirrors the Activities drawer's "Copy failure report" pattern.
export interface DiagnosticsReportInput {
  generatedAt: string;
  build: UIBuildDetails | null;
  selection: UISelection | null;
  environment: UIEnvironment | null;
  envStatus: string;
  trace: UIEnvTrace | null;
  uiTrace: UITraceEntry[];
}

export function formatDiagnosticsReport(input: DiagnosticsReportInput): string {
  const lines: string[] = [`ERun diagnostics report — ${input.generatedAt}`];
  if (input.build) {
    lines.push(buildLine(input.build));
  }
  lines.push(environmentLine(input.selection, input.environment));
  if (input.envStatus) {
    lines.push(`status: ${input.envStatus}`);
  }
  lines.push('', `── erun trace — ${input.trace?.path ?? 'unavailable'} ──`);
  if (input.trace?.notice) {
    lines.push(`note: ${input.trace.notice}`);
  }
  lines.push(traceBlock(input.trace));
  lines.push('', `── UI trace (${String(input.uiTrace.length)} entries) ──`);
  lines.push(input.uiTrace.length > 0 ? formatUITrace(input.uiTrace) : 'no UI activity recorded');
  return `${lines.join('\n')}\n`;
}

function buildLine(build: UIBuildDetails): string {
  const extras = [build.commit, build.date].filter(Boolean).join(' ');
  return `app: ${build.version}${extras ? ` (${extras})` : ''}`;
}

function traceBlock(trace: UIEnvTrace | null): string {
  if (trace?.available && trace.content) {
    return trace.content.trimEnd();
  }
  return trace?.reason ?? 'no trace loaded';
}

function environmentLine(selection: UISelection | null, env: UIEnvironment | null): string {
  if (!selection) {
    return 'environment: none selected';
  }
  const extras = [env?.type, env?.runtimeVersion ? `runtime ${env.runtimeVersion}` : '']
    .filter(Boolean)
    .join(', ');
  return `environment: ${selection.tenant} / ${selection.environment}${extras ? ` (${extras})` : ''}`;
}
