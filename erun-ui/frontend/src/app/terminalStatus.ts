import type { UISelection } from '@/types';

import type {
  ClassifiedTerminalFailure,
  CloudInitProvider,
  HiddenSessionMode,
  IDEKind,
  TerminalExitSelections,
} from './model';

export function hiddenSessionBusyMessage(selection: UISelection, mode: HiddenSessionMode): string {
  if (mode === 'sshd-init') {
    return `Enabling SSHD for ${selection.tenant} / ${selection.environment}...`;
  }
  return `Running doctor for ${selection.tenant} / ${selection.environment}...`;
}

export function terminalExitHasTrackedSelection(selections: TerminalExitSelections): boolean {
  return Boolean(
    selections.sshdInitSelection ??
    selections.doctorSelection ??
    selections.openSelection ??
    selections.cloudInit,
  );
}

export function failedTerminalExitReason(
  reason: string,
  selections: TerminalExitSelections,
): string {
  const selectionReason = failedSelectionExitReason(reason, selections);
  if (selectionReason) {
    return selectionReason;
  }
  if (selections.cloudInit) {
    return `Failed to initialize ${cloudProviderLabel(selections.cloudInit)} cloud alias: ${reason}`;
  }
  return reason;
}

export function successfulTerminalExitReason(selections: TerminalExitSelections): string {
  const selectionReason = successfulSelectionExitReason(selections);
  if (selectionReason) {
    return selectionReason;
  }
  if (selections.cloudInit) {
    return `${cloudProviderLabel(selections.cloudInit)} cloud alias setup ended.`;
  }
  return 'Session ended.';
}

function cloudProviderLabel(provider: CloudInitProvider): string {
  return provider === 'cloudflare' ? 'Cloudflare' : 'AWS';
}

export function cleanTerminalOutput(value: string): string {
  return value
    .replace(/\x1B\][^\x07]*(?:\x07|\x1B\\)/g, '')
    .replace(/\x1B(?:[@-Z\\-_]|\[[0-?]*[ -/]*[@-~])/g, '')
    .replace(/\r\n/g, '\n')
    .replace(/\r/g, '\n')
    .trim();
}

export function ideOpenFailure(
  selection: UISelection,
  label: string,
  rawError: string,
): ClassifiedTerminalFailure & { copyOutput: string } {
  const cleaned = cleanTerminalOutput(rawError);
  const trimmed = rawError.trim();
  const output = cleaned !== '' ? cleaned : trimmed !== '' ? trimmed : 'Unexpected error';
  return {
    message: `Failed to open ${label} for ${selection.tenant} / ${selection.environment}`,
    detail: shortIDEOpenFailureDetail(output),
    copyOutput: output,
    action: '',
    retrySelection: null,
  };
}

export function classifiedTerminalFailure(
  rawReason: string,
  displayReason: string,
  output: string,
  openSelection?: UISelection,
): ClassifiedTerminalFailure {
  const combined = `${rawReason}\n${output}`.toLowerCase();
  const portForward = classifiedPortForwardFailure(combined, rawReason, output, openSelection);
  if (portForward) {
    return portForward;
  }
  return {
    message: displayReason,
    detail: '',
    action: '',
    retrySelection: null,
  };
}

function isPortForwardTimeoutCombined(combined: string): boolean {
  return (
    combined.includes('timed out waiting for mcp port-forward') ||
    combined.includes('timed out waiting for api port-forward')
  );
}

function extractPortForwardPort(rawReason: string, output: string): string {
  const fromReason = /127\.0\.0\.1:(\d+)/.exec(rawReason);
  if (fromReason) {
    return fromReason[1] ?? '';
  }
  const fromOutput = /127\.0\.0\.1:(\d+)/.exec(output);
  return fromOutput?.[1] ?? '';
}

function classifiedPortForwardFailure(
  combined: string,
  rawReason: string,
  output: string,
  openSelection: UISelection | undefined,
): ClassifiedTerminalFailure | null {
  if (!isPortForwardTimeoutCombined(combined)) {
    return null;
  }
  const kind = combined.includes('api port-forward') ? 'API' : 'MCP';
  const port = extractPortForwardPort(rawReason, output);
  return {
    message: port
      ? `${kind} port-forward on 127.0.0.1:${port} is still not ready`
      : `${kind} port-forward is still not ready`,
    detail: portForwardDetail(combined, kind),
    action: openSelection ? 'wait-longer' : '',
    retrySelection: openSelection ?? null,
  };
}

export function statusForTerminalOutput(output: string): string {
  const lower = output.toLowerCase();
  const rule = terminalOutputStatusRules.find((candidate) => candidate.matches(lower));
  return rule?.message(output) ?? '';
}

// decodeTerminalOutput yields plain text so the open-status parser's substring matches aren't thrown off by ANSI escapes.
export function decodeTerminalOutput(data: Uint8Array): string {
  return new TextDecoder()
    .decode(data)
    .replace(/\x1B(?:[@-Z\\-_]|\[[0-?]*[ -/]*[@-~])/g, '')
    .replace(/\r\n/g, '\n')
    .replace(/\r/g, '\n');
}

export function ideLabel(ide: IDEKind): string {
  return ide === 'vscode' ? 'VS Code' : 'IntelliJ IDEA';
}

function failedSelectionExitReason(reason: string, selections: TerminalExitSelections): string {
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
  const firstLine =
    output
      .split('\n')
      .map((line) => line.trim())
      .find(Boolean) ?? '';
  const exitStatus = /exit status \d+/.exec(firstLine)?.[0];
  if (exitStatus) {
    return exitStatus;
  }
  if (firstLine.length <= 80) {
    return firstLine;
  }
  return `${firstLine.slice(0, 77)}...`;
}

function portForwardDetail(value: string, kind: 'API' | 'MCP'): string {
  const lowerKind = kind.toLowerCase();
  if (value.includes(`local ${lowerKind} port`) && value.includes('already in use')) {
    return `Another local process is using the ${kind} port.`;
  }
  if (value.includes('pod not found')) {
    return 'The runtime pod was replaced while the app was connecting.';
  }
  if (
    value.includes('lost connection to pod') ||
    value.includes('network namespace') ||
    value.includes('sandbox')
  ) {
    return 'The runtime pod connection was lost, likely because the pod restarted.';
  }
  if (value.includes('connection refused') || value.includes('not accepting')) {
    return `The runtime pod exists, but ${kind} is not accepting connections yet.`;
  }
  return `kubectl has not exposed a reachable ${kind} endpoint yet.`;
}

const terminalOutputStatusRules: {
  matches: (lower: string) => boolean;
  message: (output: string) => string;
}[] = [
  {
    matches: (lower) => lower.includes('forwarding from 127.0.0.1:'),
    message: mcpForwardingStatusMessage,
  },
  {
    matches: (lower) => lower.includes('handling connection for'),
    message: () => 'Checking MCP endpoint readiness...',
  },
  {
    matches: (lower) => lower.includes('connection refused'),
    message: () => 'Runtime pod is not accepting MCP connections yet...',
  },
  {
    matches: (lower) =>
      lower.includes('lost connection to pod') || lower.includes('network namespace'),
    message: () => 'Runtime pod connection changed. Reconnecting MCP port-forward...',
  },
  {
    matches: (lower) => lower.includes('pod not found'),
    message: () => 'Runtime pod was replaced. Waiting for the new pod...',
  },
  {
    matches: (lower) => lower.includes('context "') && lower.includes('modified'),
    message: () => 'Configuring Kubernetes context...',
  },
  {
    matches: (lower) => lower.includes('cluster "') && lower.includes('set.'),
    message: () => 'Configuring Kubernetes cluster access...',
  },
];

function mcpForwardingStatusMessage(output: string): string {
  const port = /Forwarding from 127\.0\.0\.1:(\d+)/.exec(output)?.[1] ?? '';
  return port ? `Waiting for MCP endpoint on 127.0.0.1:${port}...` : 'Waiting for MCP endpoint...';
}
