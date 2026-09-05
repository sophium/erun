import assert from 'node:assert/strict';
import { test } from 'node:test';

import { orchestratorShellLabel } from './orchestratorShellLabel';

const START = 1_700_000_000; // unix seconds
const NOW = (START + 332) * 1000; // 5m32s later, ms

test('names the command and the elapsed time', () => {
  const label = orchestratorShellLabel('agent', 'sleep 600', START, NOW);
  assert.equal(label, 'agent has a shell running for 5m32s: sleep 600');
});

test('still names elapsed time when the command is unknown', () => {
  const label = orchestratorShellLabel('agent', '', START, NOW);
  assert.equal(label, 'agent has a shell running for 5m32s');
});
