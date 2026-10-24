import assert from 'node:assert/strict';

import { test } from 'vitest';

import type {
  UIRuntimeResourceMetric,
  UIRuntimeResourceReading,
  UIRuntimeResourceStatus,
} from '@/types';

import { runtimeResourceBounds, runtimeResourceValidation } from './runtimeResources';

function metric(free: number, formatted: string, unit: string): UIRuntimeResourceMetric {
  return { total: free, used: 0, free, unit, formatted, floored: false };
}

function reading(
  cpuFree: number,
  memoryFree: number,
  message = '',
  notice = '',
): UIRuntimeResourceReading {
  return {
    cpu: metric(cpuFree, String(cpuFree), 'cores'),
    memory: metric(memoryFree, `${memoryFree.toFixed(1)} GiB`, 'GiB'),
    message,
    notice,
  };
}

// A node whose limits overcommit allocatable (worst case zero) while requests
// leave room for the scheduler.
function reportingClusterStatus(): UIRuntimeResourceStatus {
  const schedulable = reading(
    13.5,
    22,
    'Right now on erun-node1 (the emptiest node): the scheduler can admit 13.5 CPU and 22.0 GiB memory more.',
  );
  const worstCase = reading(
    0,
    0,
    'Worst case, with every container on erun-node1 at its declared limit at once: 0 CPU and 0.0 GiB memory left.',
    'Oversubscription headroom is managed with a namespace quota or by running fewer environments on this node.',
  );
  return {
    kubernetesContext: 'erun-node1',
    available: true,
    node: 'erun-node1',
    floored: true,
    measuredUsage: true,
    schedulable,
    schedulableComplete: true,
    worstCase,
    unmeasuredContainers: 2,
    nodes: [{ name: 'erun-node1', schedulable, schedulableComplete: true, worstCase }],
  };
}

test('a limits-saturated node does not refuse values the scheduler would place', () => {
  const status = reportingClusterStatus();
  const entered = { cpu: '4', memory: '8.7' };

  // Entered values size limits, which reserve nothing; the scheduler admits on requests.
  const validation = runtimeResourceValidation(entered, status);
  assert.equal(validation.blockingError, '');
  assert.equal(validation.capacityWarning, '');

  const bounds = runtimeResourceBounds(status, false);
  assert.ok(
    bounds.cpuMax >= 4,
    `expected a CPU maximum at or above the entered 4, got ${String(bounds.cpuMax)}`,
  );
  assert.ok(
    bounds.memoryMax >= 8.7,
    `expected a memory maximum at or above the entered 8.7, got ${String(bounds.memoryMax)}`,
  );
});

test('the worst-case reading is carried, labelled, and kept apart from scheduling', () => {
  const bounds = runtimeResourceBounds(reportingClusterStatus(), false);

  assert.match(bounds.message, /scheduler can admit/);
  assert.equal(bounds.notice, '');

  assert.match(bounds.worstCase.message, /declared limit at once/);
  assert.match(bounds.worstCase.notice, /namespace quota/);
  assert.doesNotMatch(bounds.worstCase.notice, /lower (the|your) request/);
});

test('the warning names node capacity, and never a smaller limit', () => {
  const exhausted = reportingClusterStatus();
  exhausted.schedulable = reading(0, 0);
  exhausted.nodes = [
    {
      name: 'erun-node1',
      schedulable: exhausted.schedulable,
      schedulableComplete: true,
      worstCase: exhausted.worstCase,
    },
  ];

  const { capacityWarning } = runtimeResourceValidation({ cpu: '4', memory: '8.7' }, exhausted);
  assert.match(capacityWarning, /No node has request capacity left/);
  assert.match(capacityWarning, /Stopping an environment nobody is using/);
  assert.doesNotMatch(capacityWarning, /lower (the|your) request/);
});

test('a node whose requests could not be read is not read as a shortfall', () => {
  const status = reportingClusterStatus();
  status.schedulableComplete = false;
  status.schedulable = reading(
    13.5,
    22,
    'Right now on erun-node1: at most 13.5 CPU and 22.0 GiB memory free -- an upper bound.',
  );
  status.nodes = [
    {
      name: 'erun-node1',
      schedulable: status.schedulable,
      schedulableComplete: false,
      worstCase: status.worstCase,
    },
  ];

  const validation = runtimeResourceValidation({ cpu: '4', memory: '8.7' }, status);
  assert.equal(validation.capacityWarning, '');
  assert.match(runtimeResourceBounds(status, false).message, /upper bound/);
});
