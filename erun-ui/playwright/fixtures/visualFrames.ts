import { createHash } from 'node:crypto';
import { readFile } from 'node:fs/promises';

import { expect } from '@playwright/test';

// Frames are operator-facing evidence, so a frame must show the state its name claims.

export interface ResponseGate {
  /** Resolves the stubbed call the handler is holding open. */
  release: () => void;
  /** Awaited by the stub; stays pending until `release()` is called. */
  held: Promise<void>;
}

/** Holds a stubbed RPC response open until the spec releases it; a sleep would race the capture. */
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
 * Asserts every frame is a distinct file: identical frames mean a capture outlived its state.
 * Returns one digest per path so callers can check every frame was read.
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
