import type { UIAppLog, UIBuildDetails, UIEnvironment, UIEnvTrace } from '@/types';

import type { OrchestratorInfo } from './slices/orchestratorsSlice';
import { formatUITrace, type UITraceEntry } from './uiTraceBuffer';

// The Diagnostics console's one-click, paste-ready bug report. It carries
// whatever evidence the current DiagnosticsContext resolves to, rather than a
// single environment-shaped report: an orchestrator session used to leave
// this reading "environment: none selected" with no trace at all (#1241),
// which is the empty-panel bug this contract exists to fix. An unavailable
// trace/log keeps its reason line — "unreachable" is itself evidence.
// Mirrors the Activities drawer's "Copy failure report".
export interface DiagnosticsLinkedEnv {
  tenant: string;
  environment: string;
  // The env's real-status label ('failed' | 'stopped' | 'runtime-stopped'),
  // or '' when nothing is known to be wrong.
  status: string;
}

export type DiagnosticsReportContext =
  | {
      kind: 'environment';
      tenant: string;
      environment: string;
      env: UIEnvironment | null;
      status: string;
      trace: UIEnvTrace | null;
    }
  | {
      kind: 'orchestrator';
      orchestrator: OrchestratorInfo;
      linkedEnvironments: DiagnosticsLinkedEnv[];
      appLog: UIAppLog | null;
    }
  | { kind: 'app'; appLog: UIAppLog | null };

export interface DiagnosticsReportInput {
  generatedAt: string;
  build: UIBuildDetails | null;
  context: DiagnosticsReportContext;
  uiTrace: UITraceEntry[];
}

export function formatDiagnosticsReport(input: DiagnosticsReportInput): string {
  const lines: string[] = [`ERun diagnostics report — ${input.generatedAt}`];
  if (input.build) {
    lines.push(buildLine(input.build));
  }
  lines.push(...contextLines(input.context));
  lines.push('', `── UI trace (${String(input.uiTrace.length)} entries) ──`);
  lines.push(input.uiTrace.length > 0 ? formatUITrace(input.uiTrace) : 'no UI activity recorded');
  return `${lines.join('\n')}\n`;
}

function buildLine(build: UIBuildDetails): string {
  const extras = [build.commit, build.date].filter(Boolean).join(' ');
  return `app: ${build.version}${extras ? ` (${extras})` : ''}`;
}

function contextLines(context: DiagnosticsReportContext): string[] {
  switch (context.kind) {
    case 'environment':
      return environmentContextLines(context);
    case 'orchestrator':
      return orchestratorContextLines(context);
    case 'app':
      return appLogLines(context.appLog);
  }
}

function environmentContextLines(
  context: Extract<DiagnosticsReportContext, { kind: 'environment' }>,
): string[] {
  const lines = [environmentLine(context.tenant, context.environment, context.env)];
  if (context.status) {
    lines.push(`status: ${context.status}`);
  }
  lines.push('', `── erun trace — ${context.trace?.path ?? 'unavailable'} ──`);
  if (context.trace?.notice) {
    lines.push(`note: ${context.trace.notice}`);
  }
  lines.push(traceBlock(context.trace));
  return lines;
}

function traceBlock(trace: UIEnvTrace | null): string {
  if (trace?.available && trace.content) {
    return trace.content.trimEnd();
  }
  return trace?.reason ?? 'no trace loaded';
}

function environmentLine(tenant: string, environment: string, env: UIEnvironment | null): string {
  const extras = [env?.type, env?.runtimeVersion ? `runtime ${env.runtimeVersion}` : '']
    .filter(Boolean)
    .join(', ');
  return `environment: ${tenant} / ${environment}${extras ? ` (${extras})` : ''}`;
}

function orchestratorContextLines(
  context: Extract<DiagnosticsReportContext, { kind: 'orchestrator' }>,
): string[] {
  const { orchestrator } = context;
  const lines = [
    `orchestrator: ${orchestrator.name} (${orchestrator.id})`,
    `status: ${orchestrator.status}${orchestrator.busy ? ', busy' : ''}`,
  ];
  if (orchestrator.shellRunning) {
    lines.push(`background shell: ${orchestrator.shellCommand || '(command unknown)'}`);
  }
  lines.push('', 'linked environments:');
  if (context.linkedEnvironments.length === 0) {
    lines.push('  (none)');
  } else {
    for (const env of context.linkedEnvironments) {
      lines.push(`  - ${env.tenant} / ${env.environment}${env.status ? ` (${env.status})` : ''}`);
    }
  }
  lines.push('', ...appLogLines(context.appLog));
  return lines;
}

function appLogLines(appLog: UIAppLog | null): string[] {
  const lines = [`── app log — ${appLog?.path ?? 'unavailable'} ──`];
  if (appLog?.available && appLog.content) {
    lines.push(appLog.content.trimEnd());
  } else {
    lines.push(appLog?.reason ?? 'no log loaded');
  }
  return lines;
}
