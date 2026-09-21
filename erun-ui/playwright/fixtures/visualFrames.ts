import { createHash } from 'node:crypto';
import { readFile } from 'node:fs/promises';

import { expect } from '@playwright/test';

/**
 * Visual-bundle capture helpers.
 *
 * Frames under `<artifact root>/test-results/<bundle>/` are operator-facing
 * evidence: a host-side orchestrator reviewing a remote environment reads the
 * synced bundle instead of driving the pod's desktop — which is why the root
 * defaults to the suite directory and only moves when a run is told to keep
 * out of its tree (fixtures/artifacts.ts). A frame whose name claims a state its
 * pixels do not show is worse than a missing frame, because the bundle then
 * looks complete while documenting a state it never captured. These helpers
 * keep the pending half of a transition open for the capture, and make frames
 * that should differ fail loudly instead of collapsing into one file.
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
 * Assert that every frame a test wrote is a distinct file.
 *
 * Byte-identical frames mean at least one capture outlived the state it was
 * meant to record, so the bundle documents a state it never saw. Checking the
 * whole set rather than one nominated pair is deliberate: the failure is a
 * capture that settles early, and which frame it collides with is an accident
 * of what else the test writes. Names both colliding paths in the failure so
 * the duplicate is visible without hashing the bundle by hand.
 *
 * Returns one digest per path, in input order, so a caller can also assert that
 * every frame it named was actually read -- a guard that checked nothing would
 * otherwise pass silently, which is the defect it exists to catch.
 */
export async function expectFramesAllDistinct(paths: string[]): Promise<string[]> {
  const digests = await Promise.all(
    paths.map(async (path): Promise<[string, string]> => [path, await frameDigest(path)]),
  );
  const seen = new Map<string, string>();
  for (const [path, digest] of digests) {
    const collidesWith = seen.get(digest);
    expect(
      collidesWith,
      `${path} and ${collidesWith} are byte-identical frames for states that differ`,
    ).toBeUndefined();
    seen.set(digest, path);
  }
  return digests.map(([, digest]) => digest);
}
