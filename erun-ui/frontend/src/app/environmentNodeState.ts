import type { UIEnvironmentNodeSnapshot } from '@/uiEnvironmentNodeTypes';

import { type CloudNodeState, cloudNodeState } from './cloudNodeStatus';

// A second row indicator about the node. It never changes the environment's own
// indicator: a running node says nothing about the environment's health.

export interface EnvironmentNodeIndicator {
  visible: boolean;
  state: CloudNodeState;
  // condition names the node: it is read out of context as the accessible label.
  condition: string;
  detail: string;
}

export interface EnvironmentNodeIndicatorInputs {
  node: UIEnvironmentNodeSnapshot | undefined;
  // An undetermined node gets a glyph only on an otherwise blank row.
  environmentIndicatorVisible: boolean;
}

export function environmentNodeIndicator(
  input: EnvironmentNodeIndicatorInputs,
): EnvironmentNodeIndicator {
  const label = environmentNodeLabel(input.node);
  const state = cloudNodeState(input.node?.status);
  return {
    // No node means nothing to indicate; a running node stays silent on the row.
    visible:
      input.node !== undefined &&
      (state === 'stopped' || state === 'pending' || !input.environmentIndicatorVisible),
    state,
    condition: environmentNodeCondition(state, label),
    detail: environmentNodeDetail(state, label),
  };
}

export function environmentNodeLabel(node: UIEnvironmentNodeSnapshot | undefined): string {
  if (!node) {
    return '';
  }
  const label = node.label?.trim() ?? '';
  return label !== '' ? label : node.name.trim();
}

function environmentNodeCondition(state: CloudNodeState, label: string): string {
  switch (state) {
    case 'stopped':
      return `Cloud node ${label} is stopped — select this environment and start it from the titlebar`;
    case 'pending':
      return `Cloud node ${label} is starting`;
    case 'running':
      return `Cloud node ${label} is running`;
    case 'unknown':
      // Not "stopped": an unobserved or stale node may be running.
      return `Cloud node ${label} could not be checked — its state is unknown`;
  }
}

function environmentNodeDetail(state: CloudNodeState, label: string): string {
  switch (state) {
    case 'stopped':
      return `${label} — stopped`;
    case 'pending':
      return `${label} — starting`;
    case 'running':
      return `${label} — running`;
    case 'unknown':
      return `${label} — state unknown`;
  }
}
