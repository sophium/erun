import type { ClassifiedTerminalFailure, HiddenSessionMode, IDEKind, TerminalExitSelections } from './model';
import type { UISelection } from '@/types';

export function hiddenSessionBusyMessage(selection: UISelection, mode: HiddenSessionMode): string {
  if (mode === 'sshd-init') {
    return `Enabling SSHD for ${selection.tenant} / ${selection.environment}...`;
  }
  return `Running doctor for ${selection.tenant} / ${selection.environment}...`;
}

export function terminalExitHasTrackedSelection(selections: TerminalExitSelections): boolean {
  return Boolean(selections.initSelection || selections.deploySelection || selections.sshdInitSelection || selections.doctorSelection || selections.openSelection || selections.cloudInit);
}

export function failedTerminalExitReason(reason: string, selections: TerminalExitSelections): string {
  const selectionReason = failedSelectionExitReason(reason, selections);
  if (selectionReason) {
    return selectionReason;
  }
  if (selections.cloudInit) {
    return `Failed to initialize AWS cloud alias: ${reason}`;
  }
  return reason;
}

export function successfulTerminalExitReason(selections: TerminalExitSelections): string {
  const selectionReason = successfulSelectionExitReason(selections);
  if (selectionReason) {
    return selectionReason;
  }
  if (selections.cloudInit) {
    return 'AWS cloud alias setup ended.';
  }
  return 'Session ended.';
}

export function cleanTerminalOutput(value: string): string {
  return value
    .replace(/\x1B\][^\x07]*(?:\x07|\x1B\\)/g, '')
    .replace(/\x1B(?:[@-Z\\-_]|\[[0-?]*[ -/]*[@-~])/g, '')
    .replace(/\r\n/g, '\n')
    .replace(/\r/g, '\n')
    .trim();
}

export function ideOpenFailure(selection: UISelection, label: string, rawError: string): ClassifiedTerminalFailure & { copyOutput: string } {
  const output = cleanTerminalOutput(rawError) || rawError.trim() || 'Unexpected error';
  return {
    message: `Failed to open ${label} for ${selection.tenant} / ${selection.environment}`,
    detail: shortIDEOpenFailureDetail(output),
    copyOutput: output,
    action: '',
    retrySelection: null,
  };
}

export function debugOutputBlock(output: string): string {
  const trimmed = output.trim();
  if (!trimmed) {
    return '';
  }
  return `${trimmed}\n`;
}

export function classifiedTerminalFailure(rawReason: string, displayReason: string, output: string, openSelection?: UISelection): ClassifiedTerminalFailure {
  const combined = `${rawReason}\n${output}`.toLowerCase();
  if (combined.includes('timed out waiting for mcp port-forward')) {
    const port = rawReason.match(/127\.0\.0\.1:(\d+)/)?.[1] || output.match(/127\.0\.0\.1:(\d+)/)?.[1] || '';
    return {
      message: port ? `MCP port-forward on 127.0.0.1:${port} is still not ready` : 'MCP port-forward is still not ready',
      detail: mcpPortForwardDetail(combined),
      action: openSelection ? 'wait-longer' : '',
      retrySelection: openSelection || null,
    };
  }
  return {
    message: displayReason,
    detail: '',
    action: '',
    retrySelection: null,
  };
}

export function statusForTerminalOutput(output: string): string {
  const lower = output.toLowerCase();
  const rule = terminalOutputStatusRules.find((candidate) => candidate.matches(lower));
  return rule?.message(output) || '';
}

export function decodeDebugOutput(data: Uint8Array): string {
  return new TextDecoder()
    .decode(data)
    .replace(/\x1B(?:[@-Z\\-_]|\[[0-?]*[ -/]*[@-~])/g, '')
    .replace(/\r\n/g, '\n')
    .replace(/\r/g, '\n');
}

export function interactivePromptIndex(output: string): number {
  const promptLabels = [
    'Git remote URL for environment',
    'CodeCommit SSH public key ID for environment',
    'Use existing SSH host config for environment',
    'Import the SSH public key above',
    'Kubernetes context for environment',
    'Container registry for environment',
    'Clear cached JetBrains Gateway backend metadata',
    'Prune unused Docker images',
    'Prune unused BuildKit cache',
    'Prune stopped Docker containers',
    'Initialize default environment',
    'Initialize tenant',
    'Select tenant',
  ];
  let match = -1;
  for (const label of promptLabels) {
    const index = output.lastIndexOf(label);
    if (index > match) {
      match = index;
    }
  }
  if (match === -1) {
    return -1;
  }
  return Math.max(output.lastIndexOf('\n', match), output.lastIndexOf('\r', match)) + 1;
}

export function trimDebugOutput(value: string): string {
  const maxLength = 80_000;
  if (value.length <= maxLength) {
    return value;
  }
  return value.slice(value.length - maxLength);
}

export function formatDebugCommand(selection: UISelection, mode: 'open' | 'init' | 'deploy' | 'sshd-init' | 'doctor' = 'open'): string {
  const args = ['erun'];
  if (selection.debug) {
    args.push('-vv');
  }
  appendDebugCommandArgs(args, selection, mode);
  return args.map(shellDebugArg).join(' ');
}

export function formatIDECommand(selection: UISelection, ide: IDEKind): string {
  const args = ['erun'];
  if (selection.debug) {
    args.push('-vv');
  }
  args.push('open', selection.tenant, selection.environment, ide === 'vscode' ? '--vscode' : '--intellij');
  return args.map(shellDebugArg).join(' ');
}

export function ideLabel(ide: IDEKind): string {
  return ide === 'vscode' ? 'VS Code' : 'IntelliJ IDEA';
}

function failedSelectionExitReason(reason: string, selections: TerminalExitSelections): string {
  if (selections.initSelection) {
    return `Failed to create ${selectionLabel(selections.initSelection)}: ${reason}`;
  }
  if (selections.deploySelection) {
    return `Failed to deploy ${selectionLabel(selections.deploySelection)}: ${reason}`;
  }
  if (selections.sshdInitSelection) {
    return `Failed to enable SSHD for ${selectionLabel(selections.sshdInitSelection)}: ${reason}`;
  }
  if (selections.doctorSelection) {
    return `Doctor failed for ${selectionLabel(selections.doctorSelection)}: ${reason}`;
  }
  if (selections.openSelection) {
    return `Failed to open ${selectionLabel(selections.openSelection)}: ${reason}`;
  }
  return '';
}

function successfulSelectionExitReason(selections: TerminalExitSelections): string {
  if (selections.initSelection) {
    return `Created ${selectionLabel(selections.initSelection)}.`;
  }
  if (selections.deploySelection) {
    return `Deployed ${selectionLabel(selections.deploySelection)}.`;
  }
  if (selections.sshdInitSelection) {
    return `Enabled SSHD for ${selectionLabel(selections.sshdInitSelection)}.`;
  }
  if (selections.doctorSelection) {
    return `Doctor finished for ${selectionLabel(selections.doctorSelection)}.`;
  }
  return '';
}

function selectionLabel(selection: UISelection): string {
  return `${selection.tenant} / ${selection.environment}`;
}

function shortIDEOpenFailureDetail(output: string): string {
  const firstLine = output.split('\n').map((line) => line.trim()).find(Boolean) || '';
  const exitStatus = firstLine.match(/exit status \d+/)?.[0];
  if (exitStatus) {
    return exitStatus;
  }
  if (firstLine.length <= 80) {
    return firstLine;
  }
  return `${firstLine.slice(0, 77)}...`;
}

function mcpPortForwardDetail(value: string): string {
  if (value.includes('local mcp port') && value.includes('already in use')) {
    return 'Another local process is using the MCP port.';
  }
  if (value.includes('pod not found')) {
    return 'The runtime pod was replaced while the app was connecting.';
  }
  if (value.includes('lost connection to pod') || value.includes('network namespace') || value.includes('sandbox')) {
    return 'The runtime pod connection was lost, likely because the pod restarted.';
  }
  if (value.includes('connection refused') || value.includes('not accepting')) {
    return 'The runtime pod exists, but MCP is not accepting connections yet.';
  }
  return 'kubectl has not exposed a reachable MCP endpoint yet.';
}

const terminalOutputStatusRules: Array<{ matches: (lower: string) => boolean; message: (output: string) => string }> = [
  { matches: (lower) => lower.includes('forwarding from 127.0.0.1:'), message: mcpForwardingStatusMessage },
  { matches: (lower) => lower.includes('handling connection for'), message: () => 'Checking MCP endpoint readiness...' },
  { matches: (lower) => lower.includes('connection refused'), message: () => 'Runtime pod is not accepting MCP connections yet...' },
  { matches: (lower) => lower.includes('lost connection to pod') || lower.includes('network namespace'), message: () => 'Runtime pod connection changed. Reconnecting MCP port-forward...' },
  { matches: (lower) => lower.includes('pod not found'), message: () => 'Runtime pod was replaced. Waiting for the new pod...' },
  { matches: (lower) => lower.includes('context "') && lower.includes('modified'), message: () => 'Configuring Kubernetes context...' },
  { matches: (lower) => lower.includes('cluster "') && lower.includes('set.'), message: () => 'Configuring Kubernetes cluster access...' },
];

function mcpForwardingStatusMessage(output: string): string {
  const port = output.match(/Forwarding from 127\.0\.0\.1:(\d+)/)?.[1] || '';
  return port ? `Waiting for MCP endpoint on 127.0.0.1:${port}...` : 'Waiting for MCP endpoint...';
}

function appendDebugCommandArgs(args: string[], selection: UISelection, mode: 'open' | 'init' | 'deploy' | 'sshd-init' | 'doctor'): void {
  if (mode === 'init') {
    appendInitDebugArgs(args, selection);
    return;
  }
  if (mode === 'deploy') {
    appendDeployDebugArgs(args, selection);
    return;
  }
  if (mode === 'sshd-init') {
    args.push('sshd', 'init', selection.tenant, selection.environment);
    return;
  }
  if (mode === 'doctor') {
    args.push('doctor', selection.tenant, selection.environment);
    return;
  }
  args.push('open', selection.tenant, selection.environment);
}

function appendInitDebugArgs(args: string[], selection: UISelection): void {
  args.push('init', selection.tenant, selection.environment, '--remote');
  appendOptionalDebugArg(args, '--version', selection.version);
  appendOptionalDebugArg(args, '--runtime-image', selection.runtimeImage);
  appendOptionalDebugArg(args, '--kubernetes-context', selection.kubernetesContext);
  appendOptionalDebugArg(args, '--container-registry', selection.containerRegistry);
  args.push(`--set-default-tenant=${selection.setDefaultTenant ? 'true' : 'false'}`, '--confirm-environment=true');
  appendDebugFlag(args, '--no-git', selection.noGit);
  appendDebugFlag(args, '--bootstrap', selection.bootstrap);
}

function appendDeployDebugArgs(args: string[], selection: UISelection): void {
  args.push('open', selection.tenant, selection.environment, '--no-shell', '--no-alias-prompt');
  appendOptionalDebugArg(args, '--version', selection.version);
  appendOptionalDebugArg(args, '--runtime-image', selection.runtimeImage);
}

function appendOptionalDebugArg(args: string[], name: string, value: string | undefined): void {
  if (value) {
    args.push(name, value);
  }
}

function appendDebugFlag(args: string[], name: string, enabled: boolean | undefined): void {
  if (enabled) {
    args.push(name);
  }
}

function shellDebugArg(value: string): string {
  if (/^[A-Za-z0-9._/:=-]+$/.test(value)) {
    return value;
  }
  return `'${value.replace(/'/g, `'\\''`)}'`;
}
