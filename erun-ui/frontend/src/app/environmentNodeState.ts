import type { UIEnvironmentNodeSnapshot } from '@/uiEnvironmentNodeTypes';

import { type CloudNodeState, cloudNodeState } from './cloudNodeStatus';

// An environment row that renders no indicator at all reads as "nothing to
// say", and three different situations produced exactly that blank: the node
// behind it is stopped, its port-forward never came up, and its status check
// failed. They need opposite actions, and the first — the common, cheapest one —
// was already measured and cached per cloud context and simply never carried
// per environment.
//
// This is the derivation for a SECOND indicator on the row, about the node
// rather than the environment. It never rewrites the environment's own
// indicator: a running node says nothing about whether the environment inside
// it is healthy, and letting a node reading upgrade an undetermined environment
// to "running" would trade one confident wrong answer for another.

export interface EnvironmentNodeIndicator {
  visible: boolean;
  state: CloudNodeState;
  // condition names the NODE, not the environment, and pairs the state with the
  // way out of it. It is read out of context as the indicator's accessible label
  // and tooltip, so a bare "Stopped" there would be read as the environment.
  condition: string;
  // detail is the same state without the recovery, for the hover card's Node
  // row, which already names the node beside it.
  detail: string;
}

export interface EnvironmentNodeIndicatorInputs {
  node: UIEnvironmentNodeSnapshot | undefined;
  // environmentIndicatorVisible is whether the row's own environment indicator
  // is saying anything. An undetermined node is only worth a glyph on a row
  // that is otherwise blank — a row that already reports a condition must not
  // gain a second, weaker claim beside it.
  environmentIndicatorVisible: boolean;
}

export function environmentNodeIndicator(
  input: EnvironmentNodeIndicatorInputs,
): EnvironmentNodeIndicator {
  const label = environmentNodeLabel(input.node);
  const state = cloudNodeState(input.node?.status);
  return {
    // No node at all is the definite "this environment is not backed by a node
    // erun manages" — there is nothing to indicate, and a glyph here would
    // invent a machine. A running node is the ordinary case and stays silent on
    // the row; the hover card still names it, so "the node is fine, the
    // environment is what cannot be determined" is answerable.
    visible:
      input.node !== undefined &&
      (state === 'stopped' || state === 'pending' || !input.environmentIndicatorVisible),
    state,
    condition: environmentNodeCondition(state, label),
    detail: environmentNodeDetail(state, label),
  };
}

// environmentNodeLabel prefers the operator-facing kubernetes-context name the
// Go side resolved, falling back to the node's own name.
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
      // Not "stopped". The poller either has not observed this node yet or its
      // last known-good reading went stale, and saying stopped for either would
      // send the operator to start a machine that may already be running.
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
