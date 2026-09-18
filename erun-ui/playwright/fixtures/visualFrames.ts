import { createHash } from 'node:crypto';
import { readFile } from 'node:fs/promises';

import { expect } from '@playwright/test';

/**
 * Visual-bundle capture helpers.
 *
 * Frames under `test-results/<bundle>/` are operator-facing evidence: a
 * host-side orchestrator reviewing a remote environment reads the synced bundle
 * instead of driving the pod's desktop. A frame whose name claims a state its
 * pixels do not show is worse than a missing frame, because the bundle then
 * looks complete while documenting a state it never captured. These helpers
 * keep the pending half of a transition open for the capture, and make two
 * frames that should differ fail loudly instead of collapsing into one file.
 */

export interface ResponseGate {
  /** Resolves the stubbed call the handler is holding open. */
  release: () => void;
  /** Awaited by the stub; stays pending until `release()` is called. */
  held: Promise<void>;
}

/**
 * Hold a stubbed RPC response open until the spec releases it.
 *
 * A wall-clock sleep is a race against the screenshot: the response can land
 * while the capture is still in flight, and the frame written for the pending
 * state then records the settled one. Gating on an explicit release makes the
 * in-flight window as wide as the capture needs, rather than as wide as a
 * guessed interval -- the same preference for controlling the clock over
 * waiting on it that the suite's determinism rules already require.
 */
export function holdResponse(): ResponseGate {
  let release!: () => void;
  const held = new Promise<void>((resolve) => {
    release = resolve;
  });
  return { release, held };
}

function frameDigest(path: string): Promise<string> {
  return readFile(path).then((bytes) => createHash('sha256').update(bytes).digest('hex'));
}

/**
 * Assert two captured frames are different files.
 *
 * Frames named for different states must not hash equal: byte-identical frames
 * mean the capture outlived the state it was meant to record. Names both paths
 * in the failure so the duplicated pair is visible without hashing the bundle
 * by hand.
 */
export async function expectDistinctFrames(a: string, b: string): Promise<void> {
  const [digestA, digestB] = await Promise.all([frameDigest(a), frameDigest(b)]);
  expect(digestA, `${a} and ${b} are byte-identical frames for states that differ`).not.toBe(
    digestB,
  );
}
